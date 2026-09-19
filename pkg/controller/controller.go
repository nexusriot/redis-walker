package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	log "github.com/sirupsen/logrus"

	"github.com/nexusriot/redis-walker/pkg/format"
	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
	"github.com/rivo/tview"
)

// upItem is the label of the "go to parent" entry that is always first.
const upItem = ".."

// valueMode selects how a value is rendered in the details pane.
type valueMode int

const (
	valueAuto valueMode = iota
	valueRaw
	valueHex
)

func (m valueMode) String() string {
	switch m {
	case valueRaw:
		return "raw"
	case valueHex:
		return "hex"
	default:
		return "decoded"
	}
}

// ConnectFunc opens an additional connection, used for the second pane and for
// switching database.
type ConnectFunc func(ctx context.Context, db int) (*model.Model, error)

// Controller wires the Redis model to the tview UI.
type Controller struct {
	debug bool
	view  *view.View

	panes  []*pane
	active int

	job        *job
	idleStatus string
	valueMode  valueMode

	connect ConnectFunc
	editor  string
}

// New creates a controller for the given model and view.
func New(m *model.Model, v *view.View, debug bool) *Controller {
	c := &Controller{
		debug: debug,
		view:  v,
	}
	c.panes = []*pane{
		newPane(0, m, v.Lists[0], false),
		newPane(1, m, v.Lists[1], false),
	}
	return c
}

// NewController creates the view and the controller for a Redis endpoint.
func NewController(m *model.Model, host, port string, db int, debug bool) *Controller {
	v := view.NewView()
	v.SetHeader(fmt.Sprintf("Redis-walker %s (on %s:%s, db=%d)", view.Version, host, port, db))
	return New(m, v, debug)
}

// SetConnect installs the factory used to open further connections.
func (c *Controller) SetConnect(fn ConnectFunc) { c.connect = fn }

// SetEditor overrides the external editor command; empty means $VISUAL/$EDITOR.
func (c *Controller) SetEditor(cmd string) { c.editor = cmd }

func (c *Controller) dbg(msg string, fields log.Fields) {
	if !c.debug {
		return
	}
	log.WithFields(fields).Debug(msg)
}

// cur is the pane the user is working in.
func (c *Controller) cur() *pane { return c.panes[c.active] }

// other is the pane that is not active.
func (c *Controller) other() *pane { return c.panes[1-c.active] }

func displayName(base string, isDir bool) string {
	if isDir {
		return base + "/"
	}
	return base
}

// sanitize makes an arbitrary Redis value safe to print in a tview widget:
// colour tags are escaped and control characters are made visible.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == utf8.RuneError:
			b.WriteString("�")
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\x%02x", r)
		default:
			b.WriteRune(r)
		}
	}
	return tview.Escape(b.String())
}

// editable reports whether a node can be opened in the value editor.
func editable(n *model.Node) error {
	if n.IsDir {
		return errors.New("directories have no value")
	}
	if n.Type != "" && n.Type != model.TypeString {
		return fmt.Errorf("%s keys cannot be edited by redis-walker", n.Type)
	}
	return nil
}

func humanTTL(d time.Duration) string {
	if d < 0 {
		return "none"
	}
	return d.Truncate(time.Second).String()
}

// humanBytes renders a byte count in a compact form.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTP"[exp])
}

// Run starts the UI event loop.
func (c *Controller) Run() error {
	for _, p := range c.panes {
		p.list.SetChangedFunc(func(_ int, _ string, secondary string, _ rune) {
			c.fillDetails(strings.TrimSpace(secondary))
		})
	}
	c.setInput()
	c.reload(nil)
	return c.view.App.Run()
}

// Stop ends the application and releases the connections it opened.
func (c *Controller) Stop() {
	log.Debug("exit...")
	c.Cancel()
	for _, p := range c.panes {
		if p.owned && p.model != nil {
			_ = p.model.Close()
			p.owned = false
		}
	}
	c.view.App.Stop()
}

// CurrentPrefix exposes the directory of the active pane.
func (c *Controller) CurrentPrefix() string { return c.cur().prefix }

// reload re-reads the active pane and runs after() once the list is rebuilt.
func (c *Controller) reload(after func()) { c.reloadPane(c.cur(), after) }

func (c *Controller) reloadPane(p *pane, after func()) {
	prefix := p.prefix
	desc := "listing " + model.DisplayDir(prefix)
	c.dbg("reload", log.Fields{"pane": p.idx, "prefix": prefix})

	ok := runAsync(c, desc,
		func(ctx context.Context, report func(string)) (*model.Listing, error) {
			progress := throttled(report, func(done int) string {
				return fmt.Sprintf("%d keys scanned", done)
			})
			return p.model.LsWithProgress(ctx, prefix, progress)
		},
		func(listing *model.Listing, err error) {
			if err != nil {
				p.nodes = make(map[string]*model.Node)
				p.ordered = nil
				c.renderPane(p, false)
				c.error("Failed to list keys", err, false)
				return
			}
			p.setNodes(listing.Nodes)
			c.renderPane(p, listing.Truncated)
			if after != nil {
				after()
			}
		})
	if !ok && after != nil {
		after()
	}
}

// renderPane rebuilds the list widget of a pane from its nodes.
func (c *Controller) renderPane(p *pane, truncated bool) {
	p.list.Clear()

	title := "[ [::b]" + tview.Escape(p.dir()) + "[::-] ]"
	if c.view.Dual() {
		title = fmt.Sprintf("[ [::b]%s[::-] ] %s", tview.Escape(p.dir()), tview.Escape(p.model.Endpoint()))
	}
	if truncated {
		title += " [red](truncated)[-]"
	}
	p.list.SetTitle(title)

	p.list.AddItem("[..]", upItem, 0, func() { c.upIn(p) })

	dirKeys, fileKeys := p.sortedMapKeys()
	for _, mk := range dirKeys {
		n := p.nodes[mk]
		base := model.BaseOf(n.Key)
		p.list.AddItem(colorize(base, "📁 "+tview.Escape(displayName(base, true))), mk, 0, func() {
			if cur, ok := p.selected(); ok && cur.IsDir {
				c.downIn(p, cur)
			}
		})
	}
	for _, mk := range fileKeys {
		n := p.nodes[mk]
		base := model.BaseOf(n.Key)
		label := colorize(base, "   "+tview.Escape(displayName(base, false)))
		if n.Type != "" && n.Type != model.TypeString {
			label += " [blue](" + n.Type + ")[-]"
		}
		p.list.AddItem(label, mk, 0, func() {})
	}

	ordered := make([]string, 0, len(dirKeys)+len(fileKeys))
	for _, mk := range dirKeys {
		ordered = append(ordered, displayName(model.BaseOf(p.nodes[mk].Key), true))
	}
	for _, mk := range fileKeys {
		ordered = append(ordered, displayName(model.BaseOf(p.nodes[mk].Key), false))
	}
	p.ordered = ordered

	if pos, ok := p.position[p.prefix]; ok {
		if pos >= p.list.GetItemCount() {
			pos = p.list.GetItemCount() - 1
		}
		p.list.SetCurrentItem(pos)
		delete(p.position, p.prefix)
	}
}

func colorize(base string, label string) string {
	if strings.HasPrefix(base, "_") {
		return "[yellow]" + label + "[-]"
	}
	return label
}

// selected returns the node under the cursor of the active pane.
func (c *Controller) selected() (*model.Node, bool) { return c.cur().selected() }

// focus moves the cursor of the active pane to a display label.
func (c *Controller) focus(label string) { c.focusIn(c.cur(), label) }

func (c *Controller) focusIn(p *pane, label string) {
	pos := p.indexOf(label)
	if pos < 0 {
		return
	}
	p.list.SetCurrentItem(pos)
	if p == c.cur() {
		_, mk := p.list.GetItemText(pos)
		c.fillDetails(strings.TrimSpace(mk))
	}
}

func (c *Controller) fillDetails(mapKey string) {
	c.view.Details.Clear()
	p := c.cur()
	n, ok := p.nodes[mapKey]
	if !ok {
		return
	}

	out := c.view.Details
	fmt.Fprintf(out, "[green] Path: [white] %s\n", sanitize(n.Name))
	fmt.Fprintf(out, "[green] Redis key: [white] %s\n", sanitize(n.Key))
	if n.Type != "" {
		fmt.Fprintf(out, "[green] Type: [white] %s\n", n.Type)
	}

	if n.IsDir {
		c.writeDirDetails(n)
		return
	}
	if n.Type != "" && n.Type != model.TypeString {
		fmt.Fprintf(out, "\n[yellow] (%s values are not displayed)[-]\n", n.Type)
		return
	}

	fmt.Fprintf(out, "[green] Size: [white] %s (%d bytes)\n", humanBytes(n.Size), n.Size)
	fmt.Fprintf(out, "[green] TTL: [white] %s\n", humanTTL(n.TTL))

	res := format.Detect(n.Value)
	mode := c.valueMode
	if mode == valueAuto && !res.Printable && !res.Changed {
		mode = valueHex
	}
	if n.Truncated {
		mode = valueRaw
		if c.valueMode == valueHex {
			mode = valueHex
		}
	}

	switch {
	case mode == valueAuto && res.Changed:
		fmt.Fprintf(out, "[green] Encoding: [white] %s\n", res.Describe())
	case mode == valueHex:
		fmt.Fprintf(out, "[green] View: [white] hex\n")
	default:
		fmt.Fprintf(out, "[green] View: [white] raw (%s)\n", res.Kind)
	}
	fmt.Fprintln(out)

	switch mode {
	case valueHex:
		fmt.Fprintf(out, "%s", tview.Escape(format.HexDump(n.Value, 16)))
	case valueAuto:
		fmt.Fprintf(out, "[green] Value: [white]\n%s\n", sanitize(res.Decoded))
	default:
		fmt.Fprintf(out, "[green] Value: [white]\n%s\n", sanitize(n.Value))
	}

	if n.Truncated {
		fmt.Fprintf(out, "\n[yellow] ... truncated, %s of %s shown; open the key to see all of it[-]\n",
			humanBytes(int64(len(n.Value))), humanBytes(n.Size))
	}
}

// writeDirDetails prints what is known about a folder, including the cached
// result of the last analysis.
func (c *Controller) writeDirDetails(n *model.Node) {
	out := c.view.Details
	st := c.cur().stats[model.PrefixOf(n.Key)]
	if st == nil {
		fmt.Fprintf(out, "[green] Is directory: [white] true\n")
		fmt.Fprintf(out, "\n[::d] Press Ctrl+A to analyze this folder.[::-]\n")
		return
	}
	fmt.Fprintf(out, "\n%s", statsSummary(st))
	fmt.Fprintf(out, "\n[::d] Ctrl+A re-reads these numbers.[::-]\n")
}

// statsSummary renders the headline numbers of an analysis.
func statsSummary(st *model.DirStats) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[green] Keys: [white] %d\n", st.Keys)
	fmt.Fprintf(&b, "[green] Folders: [white] %d\n", st.Folders)
	fmt.Fprintf(&b, "[green] Memory: [white] %s", humanBytes(st.Bytes))
	if st.Estimated {
		b.WriteString(" [yellow](estimated)[-]")
	}
	b.WriteString("\n")
	if len(st.Types) > 0 {
		types := make([]string, 0, len(st.Types))
		for _, t := range []string{"string", "hash", "list", "set", "zset", "stream"} {
			if n, ok := st.Types[t]; ok {
				types = append(types, fmt.Sprintf("%s %d", t, n))
			}
		}
		for t, n := range st.Types {
			if !knownType(t) {
				types = append(types, fmt.Sprintf("%s %d", t, n))
			}
		}
		fmt.Fprintf(&b, "[green] Types: [white] %s\n", strings.Join(types, ", "))
	}
	fmt.Fprintf(&b, "[green] With TTL: [white] %d", st.WithTTL)
	if st.WithTTL > 0 {
		fmt.Fprintf(&b, " (soonest %s)", humanTTL(st.SoonestTTL))
	}
	b.WriteString("\n")
	if st.Truncated {
		b.WriteString("[red] The subtree was too large to scan completely.[-]\n")
	}
	return b.String()
}

func knownType(t string) bool {
	switch t {
	case "string", "hash", "list", "set", "zset", "stream":
		return true
	}
	return false
}

func (c *Controller) setInput() {
	c.view.App.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlQ {
			c.Stop()
			return nil
		}
		return event
	})

	capture := func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			c.Cancel()
			return nil
		case tcell.KeyCtrlN:
			return c.create()
		case tcell.KeyDelete:
			return c.delete()
		case tcell.KeyCtrlE:
			return c.editSelected()
		case tcell.KeyCtrlO:
			return c.editExternally()
		case tcell.KeyCtrlS:
			return c.search()
		case tcell.KeyCtrlJ:
			return c.jump()
		case tcell.KeyCtrlR:
			c.Refresh()
			return nil
		case tcell.KeyCtrlA:
			return c.analyze()
		case tcell.KeyCtrlV:
			c.cycleValueMode()
			return nil
		case tcell.KeyCtrlD:
			return c.selectDatabase()
		case tcell.KeyCtrlW, tcell.KeyF9:
			c.ToggleDual()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			c.SwitchPane()
			return nil
		case tcell.KeyF5:
			return c.transfer(false)
		case tcell.KeyF6:
			return c.transfer(true)
		case tcell.KeyF1:
			return c.showHelp()
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			c.Up()
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case '/':
				return c.search()
			case '?':
				return c.showHelp()
			}
		}
		return event
	}
	for _, p := range c.panes {
		p.list.SetInputCapture(capture)
	}
}

func (c *Controller) downIn(p *pane, n *model.Node) {
	if n == nil || !n.IsDir {
		return
	}
	p.position[p.prefix] = p.list.GetCurrentItem()
	c.dbg("navigate down", log.Fields{"from": p.prefix, "to": model.PrefixOf(n.Key)})
	p.prefix = model.PrefixOf(n.Key)
	c.reloadPane(p, nil)
}

// Up moves the active pane to the parent directory.
func (c *Controller) Up() { c.upIn(c.cur()) }

func (c *Controller) upIn(p *pane) {
	if p.prefix == "" {
		return
	}
	child := strings.TrimSuffix(p.prefix, "/")
	p.prefix = model.ParentPrefix(p.prefix)
	c.dbg("navigate up", log.Fields{"to": p.prefix})
	c.reloadPane(p, func() {
		c.focusIn(p, displayName(model.BaseOf(child), true))
	})
}

// Refresh re-reads the current directory of the active pane.
func (c *Controller) Refresh() {
	p := c.cur()
	p.position[p.prefix] = p.list.GetCurrentItem()
	c.reloadPane(p, nil)
}

// SwitchPane moves the focus to the other pane.
func (c *Controller) SwitchPane() {
	if !c.view.Dual() {
		return
	}
	c.active = 1 - c.active
	c.view.SetActive(c.active)
	if n, ok := c.selected(); ok {
		c.fillDetails(mapKeyOf(n))
	} else {
		c.view.Details.Clear()
	}
}

// ToggleDual shows or hides the second pane.
func (c *Controller) ToggleDual() {
	if c.view.Dual() {
		c.view.SetDual(false)
		c.active = 0
		c.view.SetActive(0)
		c.renderPane(c.panes[0], false)
		return
	}

	p := c.panes[1]
	if p.model == nil {
		p.model = c.panes[0].model
	}
	if p.prefix == "" {
		p.prefix = c.panes[0].prefix
	}
	c.view.SetDual(true)
	c.renderPane(c.panes[0], false)
	c.reloadPane(p, nil)
}

// Dual reports whether the second pane is visible.
func (c *Controller) Dual() bool { return c.view.Dual() }

// cycleValueMode switches between the decoded, raw and hex value views.
func (c *Controller) cycleValueMode() {
	c.valueMode = (c.valueMode + 1) % 3
	c.setIdleStatus("value view: " + c.valueMode.String())
	if n, ok := c.selected(); ok {
		c.fillDetails(mapKeyOf(n))
	}
}

// showHelp opens the hotkeys modal and wires closing + focus restore.
func (c *Controller) showHelp() *tcell.EventKey {
	help := c.view.NewHotkeysModal()
	modal := c.view.ModalEdit(help, 74, 34)

	help.SetInputCapture(func(_ *tcell.EventKey) *tcell.EventKey {
		c.view.Pages.RemovePage("modal-help")
		c.view.App.SetFocus(c.view.List)
		return nil
	})

	c.view.Pages.AddPage("modal-help", modal, true, true)
	c.view.App.SetFocus(help)
	return nil
}

func (c *Controller) closeModal(name string) {
	c.view.Pages.RemovePage(name)
	c.view.App.SetFocus(c.view.List)
}

func (c *Controller) error(header string, err error, fatal bool) {
	errMsg := c.view.NewErrorMessageQ(header, err.Error())
	errMsg.SetDoneFunc(func(_ int, _ string) {
		c.closeModal("modal-error")
		if fatal {
			c.view.App.Stop()
		}
	})
	c.view.Pages.AddPage("modal-error", c.view.ModalEdit(errMsg, 70, 12), true, true)
}
