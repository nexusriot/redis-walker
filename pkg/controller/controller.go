package controller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	log "github.com/sirupsen/logrus"

	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
	"github.com/rivo/tview"
)

type Controller struct {
	debug        bool
	view         *view.View
	model        *model.Model
	currentDir   string
	currentNodes map[string]*Node
	position     map[string]int
}

type Node struct {
	node *model.Node
}

func splitFunc(r rune) bool { return r == '/' }

func NewController(m *model.Model, host, port string, db int, debug bool) *Controller {
	v := view.NewView()
	v.Frame.AddText(
		fmt.Sprintf("Redis-walker v.0.0.2 (preview) (on %s:%s, db=%d)", host, port, db),
		true, tview.AlignCenter, tcell.ColorGreen,
	)

	return &Controller{
		debug:        debug,
		view:         v,
		model:        m,
		currentDir:   "/",
		currentNodes: make(map[string]*Node),
		position:     make(map[string]int),
	}
}

func (c *Controller) dbg(msg string, fields log.Fields) {
	if !c.debug {
		return
	}
	log.WithFields(fields).Debug(msg)
}

// makeMapKey ensures uniqueness when file and dir share the same basename.
func makeMapKey(base string, isDir bool) string {
	if isDir {
		return base + "|dir"
	}
	return base + "|file"
}

func displayName(base string, isDir bool) string {
	if isDir {
		return base + "/"
	}
	return base
}

func normAbs(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	if p != "/" {
		p = strings.TrimRight(p, "/")
	}
	return p
}

func parentOf(p string) string {
	p = normAbs(p)
	if p == "/" {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

func baseOf(p string) string {
	p = normAbs(p)
	if p == "/" {
		return "/"
	}
	i := strings.LastIndex(p, "/")
	if i < 0 || i == len(p)-1 {
		return p
	}
	return p[i+1:]
}

func (c *Controller) makeNodeMap() error {
	c.dbg("makeNodeMap start", log.Fields{"dir": c.currentDir})
	m := make(map[string]*Node)

	list, err := c.model.Ls(c.currentDir)
	if err != nil {
		return err
	}
	for _, n := range list {
		rawName := n.Name
		fields := strings.FieldsFunc(strings.TrimSpace(rawName), splitFunc)
		base := fields[len(fields)-1]
		mapKey := makeMapKey(base, n.IsDir)
		cNode := Node{node: n}
		m[mapKey] = &cNode
		c.dbg("node seen", log.Fields{
			"name":   n.Name,
			"base":   base,
			"mapKey": mapKey,
			"is_dir": n.IsDir,
		})
	}
	c.currentNodes = m
	c.dbg("makeNodeMap done", log.Fields{"dir": c.currentDir, "count": len(m)})
	return nil
}

func (c *Controller) colorize(base string, isDir bool, label string) string {
	if strings.HasPrefix(base, "_") {
		return "[yellow]" + label + "[-]"
	}
	return label
}

func (c *Controller) updateList() []string {
	c.dbg("updateList", log.Fields{"dir": c.currentDir})
	c.view.List.Clear()
	c.view.List.SetTitle("[ [::b]" + c.currentDir + "[::-] ]")

	if err := c.makeNodeMap(); err != nil {
		c.error("failed to load keys", err, true)
	}

	// [..] always on top
	c.view.List.AddItem("[..]", "..", 0, func() {
		c.Up()
	})

	dirKeys := make([]string, 0, len(c.currentNodes))
	fileKeys := make([]string, 0, len(c.currentNodes))
	for mk := range c.currentNodes {
		if strings.HasSuffix(mk, "|dir") {
			dirKeys = append(dirKeys, mk)
		} else {
			fileKeys = append(fileKeys, mk)
		}
	}
	sort.Strings(dirKeys)
	sort.Strings(fileKeys)

	for _, mk := range dirKeys {
		n := c.currentNodes[mk].node
		fields := strings.FieldsFunc(n.Name, splitFunc)
		base := fields[len(fields)-1]
		rawLabel := "📁 " + displayName(base, true)
		label := c.colorize(base, true, rawLabel)
		c.view.List.AddItem(label, mk, 0, func() {
			i := c.view.List.GetCurrentItem()
			_, curMK := c.view.List.GetItemText(i)
			curMK = strings.TrimSpace(curMK)
			if val, ok := c.currentNodes[curMK]; ok && val.node.IsDir {
				c.position[c.currentDir] = c.view.List.GetCurrentItem()
				fields := strings.FieldsFunc(val.node.Name, splitFunc)
				base := fields[len(fields)-1]
				c.Down(base)
			}
		})
	}

	for _, mk := range fileKeys {
		n := c.currentNodes[mk].node
		fields := strings.FieldsFunc(n.Name, splitFunc)
		base := fields[len(fields)-1]
		rawLabel := "   " + displayName(base, false)
		label := c.colorize(base, false, rawLabel)
		c.view.List.AddItem(label, mk, 0, func() {
			// no-op, details are updated via SetChangedFunc
		})
	}

	if val, ok := c.position[c.currentDir]; ok {
		c.view.List.SetCurrentItem(val)
		delete(c.position, c.currentDir)
	}

	ordered := make([]string, 0, len(dirKeys)+len(fileKeys))
	for _, mk := range dirKeys {
		n := c.currentNodes[mk].node
		fs := strings.FieldsFunc(n.Name, splitFunc)
		ordered = append(ordered, displayName(fs[len(fs)-1], true))
	}
	for _, mk := range fileKeys {
		n := c.currentNodes[mk].node
		fs := strings.FieldsFunc(n.Name, splitFunc)
		ordered = append(ordered, displayName(fs[len(fs)-1], false))
	}
	return ordered
}

func (c *Controller) fillDetails(mapKey string) {
	c.view.Details.Clear()
	if val, ok := c.currentNodes[mapKey]; ok {
		log.Debugf("Node details name: %s, isDir: %t", val.node.Name, val.node.IsDir)
		fmt.Fprintf(c.view.Details, "[green] Full name: [white] %s\n", val.node.Name)
		fmt.Fprintf(c.view.Details, "[green] Is directory: [white] %t\n\n", val.node.IsDir)
		if !val.node.IsDir {
			fmt.Fprintf(c.view.Details, "[green] Value: [white]\n%s\n", val.node.Value)
		}
	}
}

func (c *Controller) getPosition(element string, slice []string) int {
	for k, v := range slice {
		if element == v {
			return k
		}
	}
	return 0
}

// showHelp opens the hotkeys modal and wires closing + focus restore.
func (c *Controller) showHelp() *tcell.EventKey {
	help := c.view.NewHotkeysModal()

	modal := c.view.ModalEdit(help, 70, 18)

	// Close on any key and restore focus to the list
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
		switch event.Key() {
		case tcell.KeyCtrlQ:
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
			return c.editMultiline()
		case tcell.KeyCtrlS:
			return c.search()
		case tcell.KeyCtrlJ:
			return c.jump()
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
				// Reliable help key
				return c.showHelp()
			}
		}
		return event
	})
}

func (c *Controller) Down(cur string) {
	var newDir string
	if c.currentDir == "/" {
		newDir = "/" + strings.TrimPrefix(cur, "/") + "/"
	} else {
		newDir = strings.TrimSuffix(c.currentDir, "/") + "/" + strings.TrimPrefix(cur, "/") + "/"
	}
	c.dbg("navigate down", log.Fields{"from": c.currentDir, "to": newDir})
	c.currentDir = newDir
	c.Cd(c.currentDir)
}

func (c *Controller) Up() {
	fields := strings.FieldsFunc(strings.TrimSpace(c.currentDir), splitFunc)
	if len(fields) == 0 {
		return
	}
	newDir := "/" + strings.Join(fields[:len(fields)-1], "/")
	if len(fields) > 1 {
		newDir = newDir + "/"
	}
	c.dbg("navigate up", log.Fields{"from": c.currentDir, "to": newDir})
	c.currentDir = newDir
	c.Cd(c.currentDir)
}

func (c *Controller) Cd(path string) { c.updateList() }

func (c *Controller) Stop() {
	log.Debug("exit...")
	c.view.App.Stop()
}

func (c *Controller) Run() error {
	c.view.List.SetChangedFunc(func(i int, main string, secondary string, _ rune) {
		curMK := strings.TrimSpace(secondary)
		c.fillDetails(curMK)
	})
	c.updateList()
	c.setInput()
	return c.view.App.Run()
}

func (c *Controller) search() *tcell.EventKey {
	search := c.view.NewSearch()

	dirNames := []string{}
	fileNames := []string{}
	for mk, v := range c.currentNodes {
		fs := strings.FieldsFunc(v.node.Name, splitFunc)
		base := fs[len(fs)-1]
		if strings.HasSuffix(mk, "|dir") {
			dirNames = append(dirNames, displayName(base, true))
		} else {
			fileNames = append(fileNames, displayName(base, false))
		}
	}
	sort.Strings(dirNames)
	sort.Strings(fileNames)
	ordered := append(dirNames, fileNames...)

	search.SetDoneFunc(func(key tcell.Key) {
		oldPos := c.view.List.GetCurrentItem()
		value := strings.TrimSpace(search.GetText())
		pos := c.getPosition(value, ordered)
		if pos+1 != oldPos && key == tcell.KeyEnter {
			c.view.List.SetCurrentItem(pos + 1)
		}
		c.view.Pages.RemovePage("modal")
	})

	search.SetAutocompleteFunc(func(currentText string) []string {
		prefix := strings.TrimSpace(strings.ToLower(currentText))
		if prefix == "" {
			return nil
		}
		result := make([]string, 0, len(ordered))
		for _, word := range ordered {
			if strings.HasPrefix(strings.ToLower(word), prefix) {
				result = append(result, word)
			}
		}
		return result
	})

	c.view.Pages.AddPage("modal", c.view.ModalEdit(search, 60, 5), true, true)
	return nil
}

func (c *Controller) delete() *tcell.EventKey {
	if c.view.List.GetItemCount() == 0 {
		return nil
	}
	i := c.view.List.GetCurrentItem()
	_, mapKey := c.view.List.GetItemText(i)
	mapKey = strings.TrimSpace(mapKey)
	if mapKey == ".." {
		return nil
	}

	if val, ok := c.currentNodes[mapKey]; ok {
		base := displayName(baseOf(val.node.Name), val.node.IsDir)
		elem := base
		if val.node.IsDir {
			elem = elem + " (recursive)"
		}
		delQ := c.view.NewDeleteQ(elem)
		delQ.SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			if buttonLabel == "ok" {
				var err error
				if !val.node.IsDir {
					err = c.model.Del(val.node.Name)
				} else {
					err = c.model.DelDir(val.node.Name)
				}
				if err != nil {
					log.WithError(err).Error("delete failed")
					c.view.Pages.RemovePage("modal")
					c.error("Error deleting key", err, false)
					return
				}
				c.view.Details.Clear()
				c.updateList()
			}
			c.view.Pages.RemovePage("modal")
		})
		c.view.Pages.AddPage("modal", c.view.ModalEdit(delQ, 20, 7), true, true)
	}
	return nil
}

func (c *Controller) create() *tcell.EventKey {
	pos := 0
	createForm := c.view.NewCreateForm(fmt.Sprintf("Create Key: %s", c.currentDir))
	createForm.AddButton("Save", func() {
		key := createForm.GetFormItem(0).(*tview.InputField).GetText()
		value := createForm.GetFormItem(1).(*tview.InputField).GetText()
		isDir := createForm.GetFormItem(2).(*tview.Checkbox).IsChecked()
		if key != "" {
			full := normAbs(c.currentDir + key)
			var err error
			if !isDir {
				err = c.model.Set(full, value)
			} else {
				err = c.model.MkDir(full)
			}
			if err != nil {
				c.view.Pages.RemovePage("modal")
				c.error("Error creating key", err, false)
				return
			}
			ordered := c.updateList()
			target := key
			if isDir {
				target = key + "/"
			}
			pos = c.getPosition(target, ordered) + 1
			c.view.Pages.RemovePage("modal")
			c.view.List.SetCurrentItem(pos)
		}
	})
	createForm.AddButton("Quit", func() {
		c.view.Pages.RemovePage("modal")
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(createForm, 55, 11), true, true)
	return nil
}

func (c *Controller) edit() *tcell.EventKey {
	i := c.view.List.GetCurrentItem()
	_, mapKey := c.view.List.GetItemText(i)
	mapKey = strings.TrimSpace(mapKey)
	if mapKey == ".." {
		return nil
	}

	if val, ok := c.currentNodes[mapKey]; ok {
		if !val.node.IsDir {
			editValueForm := c.view.NewEditValueForm(fmt.Sprintf("Edit: %s", val.node.Name), val.node.Value)
			editValueForm.AddButton("Save", func() {
				value := editValueForm.GetFormItem(0).(*tview.InputField).GetText()
				if err := c.model.Set(val.node.Name, value); err != nil {
					c.view.Pages.RemovePage("modal")
					c.error(fmt.Sprintf("Failed to edit %s", val.node.Name), err, false)
					return
				}
				ordered := c.updateList()
				base := displayName(baseOf(val.node.Name), false)
				pos := c.getPosition(base, ordered) + 1
				c.view.Pages.RemovePage("modal")
				c.view.List.SetCurrentItem(pos)
			})
			editValueForm.AddButton("Quit", func() {
				c.view.Pages.RemovePage("modal")
			})
			c.view.Pages.AddPage("modal", c.view.ModalEdit(editValueForm, 60, 7), true, true)
			return nil
		}

		// rename directory
		curBase := baseOf(val.node.Name)
		editDirForm := c.view.NewEditValueForm(fmt.Sprintf("Rename folder: %s", val.node.Name), curBase)
		editDirForm.AddButton("Save", func() {
			newName := strings.TrimSpace(editDirForm.GetFormItem(0).(*tview.InputField).GetText())
			if newName == "" || strings.Contains(newName, "/") {
				c.view.Pages.RemovePage("modal")
				c.error("Invalid folder name", fmt.Errorf("name must be non-empty and must not contain '/'"), false)
				return
			}
			oldPath := val.node.Name
			newPath := normAbs(c.currentDir + newName)
			if newPath == oldPath {
				c.view.Pages.RemovePage("modal")
				return
			}
			if err := c.model.RenameDir(oldPath, newPath); err != nil {
				c.view.Pages.RemovePage("modal")
				c.error("Failed to rename folder", err, false)
				return
			}
			ordered := c.updateList()
			pos := c.getPosition(newName+"/", ordered) + 1
			c.view.Pages.RemovePage("modal")
			c.view.List.SetCurrentItem(pos)
		})
		editDirForm.AddButton("Quit", func() {
			c.view.Pages.RemovePage("modal")
		})
		c.view.Pages.AddPage("modal", c.view.ModalEdit(editDirForm, 60, 7), true, true)
	}
	return nil
}

func (c *Controller) editMultiline() *tcell.EventKey {
	if c.view.List.GetItemCount() == 0 {
		return nil
	}
	i := c.view.List.GetCurrentItem()
	_, mapKey := c.view.List.GetItemText(i)
	mapKey = strings.TrimSpace(mapKey)
	if mapKey == ".." {
		return nil
	}
	val, ok := c.currentNodes[mapKey]
	if !ok {
		return nil
	}
	if val.node.IsDir {
		return c.edit()
	}

	title := fmt.Sprintf(" Edit (multiline): %s ", val.node.Name)
	ta := c.view.NewMultilineEditor(title, val.node.Value)

	ta.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlS:
			value := ta.GetText()
			if err := c.model.Set(val.node.Name, value); err != nil {
				c.view.CloseEditor()
				c.error("Failed to save value", err, false)
				return nil
			}
			c.view.CloseEditor()
			ordered := c.updateList()
			base := displayName(baseOf(val.node.Name), false)
			pos := c.getPosition(base, ordered) + 1
			c.view.List.SetCurrentItem(pos)
			// refresh details
			i := c.view.List.GetCurrentItem()
			_, mk := c.view.List.GetItemText(i)
			c.fillDetails(strings.TrimSpace(mk))
			return nil
		case tcell.KeyEsc, tcell.KeyCtrlQ:
			c.view.CloseEditor()
			return nil
		}
		return ev
	})

	c.view.OpenEditor(ta)
	return nil
}

func (c *Controller) error(header string, err error, fatal bool) {
	errMsg := c.view.NewErrorMessageQ(header, err.Error())
	errMsg.SetDoneFunc(func(buttonIndex int, buttonLabel string) {
		c.view.Pages.RemovePage("modal")
		if fatal {
			c.view.App.Stop()
		}
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(errMsg, 8, 3), true, true)
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

		isDirHint := strings.HasSuffix(raw, "/")
		var target string
		if strings.HasPrefix(raw, "/") {
			target = normAbs(raw)
		} else {
			cur := normAbs(c.currentDir)
			if cur != "/" {
				target = normAbs(cur + "/" + raw)
			} else {
				target = normAbs("/" + raw)
			}
		}

		nd, err := c.model.Get(target)
		if err != nil {
			c.error("Not found", fmt.Errorf("%s", target), false)
			return
		}
		if isDirHint && !nd.IsDir {
			c.error("Not a folder", fmt.Errorf("%s", target), false)
			return
		}

		if nd.IsDir {
			c.currentDir = normAbs(nd.Name) + "/"
			c.Cd(c.currentDir)
			return
		}

		parent := parentOf(nd.Name)
		base := baseOf(nd.Name)
		if !strings.HasSuffix(parent, "/") {
			parent += "/"
		}
		c.currentDir = parent
		ordered := c.updateList()

		findIndex := func(name string, list []string) int {
			for i, v := range list {
				if v == name {
					return i
				}
			}
			return -1
		}

		if pos := findIndex(base, ordered); pos >= 0 {
			c.view.List.SetCurrentItem(pos + 1)
			i := c.view.List.GetCurrentItem()
			_, mk := c.view.List.GetItemText(i)
			c.fillDetails(strings.TrimSpace(mk))
			return
		}
		if pos := findIndex(base+"/", ordered); pos >= 0 {
			c.view.List.SetCurrentItem(pos + 1)
			i := c.view.List.GetCurrentItem()
			_, mk := c.view.List.GetItemText(i)
			c.fillDetails(strings.TrimSpace(mk))
			return
		}
		c.error("Not found", fmt.Errorf("%s", target), false)
	})

	c.view.Pages.AddPage("modal", c.view.ModalEdit(inp, 60, 5), true, true)
	return nil
}
