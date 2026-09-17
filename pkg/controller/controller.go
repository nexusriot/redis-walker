package controller

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	log "github.com/sirupsen/logrus"

	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
	"github.com/rivo/tview"
)

// upItem is the label of the "go to parent" entry that is always first.
const upItem = ".."

// Controller wires the Redis model to the tview UI.
type Controller struct {
	debug bool
	view  *view.View
	model *model.Model

	// currentPrefix is the *real* Redis key prefix of the directory being
	// shown ("" at the root, otherwise ending in "/").
	currentPrefix string
	// currentNodes maps a list item's hidden key to its node.
	currentNodes map[string]*model.Node
	// ordered holds the display labels in list order (without "..").
	ordered []string
	// position remembers the cursor per directory prefix.
	position map[string]int
}

// New creates a controller for the given model.
func New(m *model.Model, v *view.View, debug bool) *Controller {
	return &Controller{
		debug:         debug,
		view:          v,
		model:         m,
		currentPrefix: "",
		currentNodes:  make(map[string]*model.Node),
		position:      make(map[string]int),
	}
}

// NewController creates the view and the controller for a Redis endpoint.
func NewController(m *model.Model, host, port string, db int, debug bool) *Controller {
	v := view.NewView()
	v.SetHeader(fmt.Sprintf("Redis-walker %s (on %s:%s, db=%d)", view.Version, host, port, db))
	return New(m, v, debug)
}

func (c *Controller) dbg(msg string, fields log.Fields) {
	if !c.debug {
		return
	}
	log.WithFields(fields).Debug(msg)
}

// mapKeyOf builds a list item key that is unique even when a directory and a
// plain key share the same name ("a" and "a/b" both exist).
func mapKeyOf(n *model.Node) string {
	if n.IsDir {
		return n.Key + "|dir"
	}
	return n.Key + "|file"
}

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
	switch {
	case d < 0:
		return "none"
	default:
		return d.Truncate(time.Second).String()
	}
}

// makeNodeMap reloads the current directory. On error the previous listing is
// dropped so that stale entries are never shown for the new directory.
func (c *Controller) makeNodeMap() (truncated bool, err error) {
	c.dbg("makeNodeMap start", log.Fields{"prefix": c.currentPrefix})

	listing, err := c.model.Ls(c.currentPrefix)
	if err != nil {
		c.currentNodes = make(map[string]*model.Node)
		return false, err
	}
	m := make(map[string]*model.Node, len(listing.Nodes))
	for _, n := range listing.Nodes {
		m[mapKeyOf(n)] = n
	}
	c.currentNodes = m
	c.dbg("makeNodeMap done", log.Fields{"prefix": c.currentPrefix, "count": len(m)})
	return listing.Truncated, nil
}

// sortedMapKeys returns directory keys first, then leaf keys, each sorted by
// the name shown to the user. This is the single source of truth for the list
// order; search and "jump" rely on it.
func (c *Controller) sortedMapKeys() (dirs, files []string) {
	for mk, n := range c.currentNodes {
		if n.IsDir {
			dirs = append(dirs, mk)
		} else {
			files = append(files, mk)
		}
	}
	less := func(keys []string) func(i, j int) bool {
		return func(i, j int) bool {
			a, b := c.currentNodes[keys[i]], c.currentNodes[keys[j]]
			an, bn := model.BaseOf(a.Key), model.BaseOf(b.Key)
			if an != bn {
				return an < bn
			}
			return a.Key < b.Key
		}
	}
	sort.Slice(dirs, less(dirs))
	sort.Slice(files, less(files))
	return dirs, files
}

// updateList rebuilds the list from the current directory.
func (c *Controller) updateList() {
	c.dbg("updateList", log.Fields{"prefix": c.currentPrefix})
	c.view.List.Clear()

	truncated, err := c.makeNodeMap()
	title := "[ [::b]" + tview.Escape(model.DisplayDir(c.currentPrefix)) + "[::-] ]"
	if truncated {
		title += " [red](truncated)[-]"
	}
	c.view.List.SetTitle(title)
	if err != nil {
		c.error("Failed to list keys", err, false)
	}

	// "[..]" is always the first entry.
	c.view.List.AddItem("[..]", upItem, 0, func() { c.Up() })

	dirKeys, fileKeys := c.sortedMapKeys()

	for _, mk := range dirKeys {
		n := c.currentNodes[mk]
		base := model.BaseOf(n.Key)
		label := c.colorize(base, "📁 "+tview.Escape(displayName(base, true)))
		c.view.List.AddItem(label, mk, 0, func() {
			cur, ok := c.selected()
			if ok && cur.IsDir {
				c.position[c.currentPrefix] = c.view.List.GetCurrentItem()
				c.Down(cur)
			}
		})
	}
	for _, mk := range fileKeys {
		n := c.currentNodes[mk]
		base := model.BaseOf(n.Key)
		label := c.colorize(base, "   "+tview.Escape(displayName(base, false)))
		if n.Type != "" && n.Type != model.TypeString {
			label += " [blue](" + n.Type + ")[-]"
		}
		c.view.List.AddItem(label, mk, 0, func() {})
	}

	ordered := make([]string, 0, len(dirKeys)+len(fileKeys))
	for _, mk := range dirKeys {
		ordered = append(ordered, displayName(model.BaseOf(c.currentNodes[mk].Key), true))
	}
	for _, mk := range fileKeys {
		ordered = append(ordered, displayName(model.BaseOf(c.currentNodes[mk].Key), false))
	}
	c.ordered = ordered

	if pos, ok := c.position[c.currentPrefix]; ok {
		if pos >= c.view.List.GetItemCount() {
			pos = c.view.List.GetItemCount() - 1
		}
		c.view.List.SetCurrentItem(pos)
		delete(c.position, c.currentPrefix)
	}
}

func (c *Controller) colorize(base string, label string) string {
	if strings.HasPrefix(base, "_") {
		return "[yellow]" + label + "[-]"
	}
	return label
}

// selected returns the node under the cursor.
func (c *Controller) selected() (*model.Node, bool) {
	if c.view.List.GetItemCount() == 0 {
		return nil, false
	}
	i := c.view.List.GetCurrentItem()
	_, mk := c.view.List.GetItemText(i)
	mk = strings.TrimSpace(mk)
	if mk == upItem {
		return nil, false
	}
	n, ok := c.currentNodes[mk]
	return n, ok
}

func (c *Controller) fillDetails(mapKey string) {
	c.view.Details.Clear()
	n, ok := c.currentNodes[mapKey]
	if !ok {
		return
	}
	fmt.Fprintf(c.view.Details, "[green] Path: [white] %s\n", sanitize(n.Name))
	fmt.Fprintf(c.view.Details, "[green] Redis key: [white] %s\n", sanitize(n.Key))
	fmt.Fprintf(c.view.Details, "[green] Is directory: [white] %t\n", n.IsDir)
	if n.Type != "" {
		fmt.Fprintf(c.view.Details, "[green] Type: [white] %s\n", n.Type)
	}
	if !n.IsDir && n.Type == model.TypeString {
		fmt.Fprintf(c.view.Details, "[green] Size: [white] %d bytes\n", n.Size)
		fmt.Fprintf(c.view.Details, "[green] TTL: [white] %s\n", humanTTL(n.TTL))
	}
	fmt.Fprintln(c.view.Details)
	switch {
	case n.IsDir && n.Type == "":
		return
	case n.Type != "" && n.Type != model.TypeString:
		fmt.Fprintf(c.view.Details, "[yellow] (%s values are not displayed)[-]\n", n.Type)
	default:
		fmt.Fprintf(c.view.Details, "[green] Value: [white]\n%s\n", sanitize(n.Value))
		if n.Truncated {
			fmt.Fprintf(c.view.Details, "\n[yellow] ... truncated, %d of %d bytes shown[-]\n",
				len(n.Value), n.Size)
		}
	}
}

// indexOf returns the list index of a display label, or -1.
func (c *Controller) indexOf(label string) int {
	for i, v := range c.ordered {
		if v == label {
			return i + 1 // account for "[..]"
		}
	}
	return -1
}

// focus moves the cursor to a display label and refreshes the details pane.
func (c *Controller) focus(label string) {
	pos := c.indexOf(label)
	if pos < 0 {
		return
	}
	c.view.List.SetCurrentItem(pos)
	_, mk := c.view.List.GetItemText(pos)
	c.fillDetails(strings.TrimSpace(mk))
}

// showHelp opens the hotkeys modal and wires closing + focus restore.
func (c *Controller) showHelp() *tcell.EventKey {
	help := c.view.NewHotkeysModal()
	modal := c.view.ModalEdit(help, 74, 22)

	help.SetInputCapture(func(_ *tcell.EventKey) *tcell.EventKey {
		c.view.Pages.RemovePage("modal-help")
		c.view.App.SetFocus(c.view.List)
		return nil
	})

	c.view.Pages.AddPage("modal-help", modal, true, true)
	c.view.App.SetFocus(help)
	return nil
}

func (c *Controller) setInput() {
	c.view.App.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlQ {
			c.Stop()
			return nil
		}
		return event
	})

	c.view.List.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyCtrlN:
			return c.create()
		case tcell.KeyDelete:
			return c.delete()
		case tcell.KeyCtrlE:
			return c.editSelected()
		case tcell.KeyCtrlS:
			return c.search()
		case tcell.KeyCtrlJ:
			return c.jump()
		case tcell.KeyCtrlR:
			c.Refresh()
			return nil
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
	})
}

// Down descends into a directory node.
func (c *Controller) Down(n *model.Node) {
	if n == nil || !n.IsDir {
		return
	}
	c.dbg("navigate down", log.Fields{"from": c.currentPrefix, "to": model.PrefixOf(n.Key)})
	c.currentPrefix = model.PrefixOf(n.Key)
	c.updateList()
}

// Up moves to the parent directory.
func (c *Controller) Up() {
	if c.currentPrefix == "" {
		return
	}
	child := strings.TrimSuffix(c.currentPrefix, "/")
	parent := model.ParentPrefix(c.currentPrefix)
	c.dbg("navigate up", log.Fields{"from": c.currentPrefix, "to": parent})
	c.currentPrefix = parent
	c.updateList()
	c.focus(displayName(model.BaseOf(child), true))
}

// Refresh re-reads the current directory from Redis.
func (c *Controller) Refresh() {
	c.position[c.currentPrefix] = c.view.List.GetCurrentItem()
	c.updateList()
}

// CurrentPrefix exposes the directory being shown (used by tests).
func (c *Controller) CurrentPrefix() string { return c.currentPrefix }

func (c *Controller) Stop() {
	log.Debug("exit...")
	c.view.App.Stop()
}

// Run starts the UI event loop.
func (c *Controller) Run() error {
	c.view.List.SetChangedFunc(func(_ int, _ string, secondary string, _ rune) {
		c.fillDetails(strings.TrimSpace(secondary))
	})
	c.updateList()
	c.setInput()
	return c.view.App.Run()
}

func (c *Controller) search() *tcell.EventKey {
	search := c.view.NewSearch()

	search.SetDoneFunc(func(key tcell.Key) {
		defer c.view.Pages.RemovePage("modal")
		if key != tcell.KeyEnter {
			return
		}
		if value := strings.TrimSpace(search.GetText()); value != "" {
			c.focus(value)
		}
	})
	search.SetAutocompleteFunc(c.matchOrdered)

	c.view.Pages.AddPage("modal", c.view.ModalEdit(search, 60, 5), true, true)
	return nil
}

// matchOrdered returns the entries of the current directory that start with
// the given text, in list order.
func (c *Controller) matchOrdered(currentText string) []string {
	prefix := strings.TrimSpace(strings.ToLower(currentText))
	if prefix == "" {
		return nil
	}
	result := make([]string, 0, len(c.ordered))
	for _, word := range c.ordered {
		if strings.HasPrefix(strings.ToLower(word), prefix) {
			result = append(result, word)
		}
	}
	return result
}

func (c *Controller) delete() *tcell.EventKey {
	n, ok := c.selected()
	if !ok {
		return nil
	}

	elem := displayName(model.BaseOf(n.Key), n.IsDir)
	if n.IsDir {
		elem += " (recursive)"
	}
	delQ := c.view.NewDeleteQ(elem)
	delQ.SetDoneFunc(func(_ int, buttonLabel string) {
		c.view.Pages.RemovePage("modal")
		if buttonLabel != "ok" {
			return
		}
		if err := c.deleteNode(n); err != nil {
			log.WithError(err).Error("delete failed")
			c.error("Error deleting key", err, false)
		}
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(delQ, 44, 9), true, true)
	return nil
}

// deleteNode removes a key, or a whole subtree for a folder.
func (c *Controller) deleteNode(n *model.Node) error {
	var err error
	if n.IsDir {
		err = c.model.DelDir(n.Key)
	} else {
		err = c.model.Del(n.Key)
	}
	if err != nil {
		return err
	}
	c.view.Details.Clear()
	c.updateList()
	return nil
}

func (c *Controller) create() *tcell.EventKey {
	createForm := c.view.NewCreateForm(fmt.Sprintf("Create key in: %s", model.DisplayDir(c.currentPrefix)))
	createForm.AddButton("Save", func() {
		name := createForm.GetFormItem(0).(*tview.InputField).GetText()
		value := createForm.GetFormItem(1).(*tview.InputField).GetText()
		isDir := createForm.GetFormItem(2).(*tview.Checkbox).IsChecked()
		c.view.Pages.RemovePage("modal")
		if err := c.createEntry(name, value, isDir); err != nil {
			c.error("Error creating key", err, false)
		}
	})
	createForm.AddButton("Quit", func() {
		c.view.Pages.RemovePage("modal")
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(createForm, 60, 13), true, true)
	return nil
}

// createEntry creates a key or a folder below the current directory and moves
// the cursor onto it.
func (c *Controller) createEntry(name, value string, isDir bool) error {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		return errors.New("the key name must not be empty")
	}

	full := model.ChildKey(c.currentPrefix, name)
	var err error
	if isDir {
		err = c.model.MkDir(full)
	} else {
		err = c.model.Set(full, value)
	}
	if err != nil {
		return err
	}
	c.updateList()
	// A nested name ("a/b") shows up as a folder in the current view.
	first, _, nested := strings.Cut(name, "/")
	c.focus(displayName(first, isDir || nested))
	return nil
}

// editSelected opens the value editor for keys and the rename dialog for dirs.
func (c *Controller) editSelected() *tcell.EventKey {
	n, ok := c.selected()
	if !ok {
		return nil
	}
	if n.IsDir {
		return c.renameDir(n)
	}
	return c.editValue(n)
}

func (c *Controller) renameDir(n *model.Node) *tcell.EventKey {
	curBase := model.BaseOf(n.Key)
	form := c.view.NewEditValueForm(fmt.Sprintf("Rename folder: %s", n.Name), curBase)
	form.AddButton("Save", func() {
		newName := form.GetFormItem(0).(*tview.InputField).GetText()
		c.view.Pages.RemovePage("modal")
		if err := c.renameDirTo(n, newName); err != nil {
			c.error("Failed to rename folder", err, false)
		}
	})
	form.AddButton("Quit", func() {
		c.view.Pages.RemovePage("modal")
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(form, 60, 7), true, true)
	return nil
}

// renameDirTo renames a folder inside the current directory.
func (c *Controller) renameDirTo(n *model.Node, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" || strings.Contains(newName, "/") {
		return errors.New("the name must be non-empty and must not contain '/'")
	}
	if newName == model.BaseOf(n.Key) {
		return nil
	}
	if err := c.model.RenameDir(n.Key, model.ChildKey(c.currentPrefix, newName)); err != nil {
		return err
	}
	c.updateList()
	c.focus(displayName(newName, true))
	return nil
}

func (c *Controller) editValue(n *model.Node) *tcell.EventKey {
	full, err := c.loadForEdit(n)
	if err != nil {
		c.error("Cannot edit "+n.Name, err, false)
		return nil
	}

	ta := c.view.NewMultilineEditor(fmt.Sprintf(" Edit: %s ", full.Name), full.Value)
	ta.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlS:
			value := ta.GetText()
			c.view.CloseEditor()
			if err := c.saveValue(full, value); err != nil {
				c.error("Failed to save value", err, false)
			}
			return nil
		case tcell.KeyEsc:
			c.view.CloseEditor()
			return nil
		}
		return ev
	})

	c.view.OpenEditor(ta)
	return nil
}

// loadForEdit re-reads a key in full and refuses values that the editor would
// damage: the listing only holds a preview, so saving it back would truncate
// large values, and a TextArea cannot round-trip binary data.
func (c *Controller) loadForEdit(n *model.Node) (*model.Node, error) {
	if err := editable(n); err != nil {
		return nil, err
	}
	full, err := c.model.Get(n.Key)
	if err != nil {
		return nil, err
	}
	if err := editable(full); err != nil {
		return nil, err
	}
	if !utf8.ValidString(full.Value) {
		return nil, errors.New("the value is not valid UTF-8; editing it would corrupt binary data")
	}
	return full, nil
}

// saveValue writes an edited value back and keeps the cursor on the key.
func (c *Controller) saveValue(n *model.Node, value string) error {
	if err := c.model.Set(n.Key, value); err != nil {
		return err
	}
	c.updateList()
	c.focus(displayName(model.BaseOf(n.Key), false))
	return nil
}

func (c *Controller) error(header string, err error, fatal bool) {
	errMsg := c.view.NewErrorMessageQ(header, err.Error())
	errMsg.SetDoneFunc(func(_ int, _ string) {
		c.view.Pages.RemovePage("modal-error")
		c.view.App.SetFocus(c.view.List)
		if fatal {
			c.view.App.Stop()
		}
	})
	c.view.Pages.AddPage("modal-error", c.view.ModalEdit(errMsg, 70, 12), true, true)
}

func (c *Controller) jump() *tcell.EventKey {
	inp := c.view.NewJump()
	inp.SetDoneFunc(func(key tcell.Key) {
		defer c.view.Pages.RemovePage("modal")
		if key != tcell.KeyEnter {
			return
		}
		raw := strings.TrimSpace(inp.GetText())
		if raw == "" {
			return
		}
		c.JumpTo(raw)
	})

	c.view.Pages.AddPage("modal", c.view.ModalEdit(inp, 60, 5), true, true)
	return nil
}

// JumpTo navigates to an absolute or relative path. A trailing "/" requires the
// target to be a directory.
func (c *Controller) JumpTo(raw string) {
	isDirHint := strings.HasSuffix(raw, "/")
	target := strings.TrimSuffix(raw, "/")
	if !strings.HasPrefix(raw, "/") {
		target = c.currentPrefix + target
	}

	nd, err := c.model.Resolve(target)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			c.error("Not found", errors.New(model.PathOf(target)), false)
		} else {
			c.error("Jump failed", err, false)
		}
		return
	}
	if isDirHint && !nd.IsDir {
		c.error("Not a folder", errors.New(nd.Name), false)
		return
	}

	if nd.IsDir {
		c.currentPrefix = model.PrefixOf(nd.Key)
		c.updateList()
		return
	}

	c.currentPrefix = model.ParentPrefix(model.PrefixOf(nd.Key))
	c.updateList()
	c.focus(displayName(model.BaseOf(nd.Key), false))
}
