package controller

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	log "github.com/sirupsen/logrus"

	"github.com/nexusriot/redis-walker/pkg/format"
	"github.com/nexusriot/redis-walker/pkg/model"
)

// search finds an entry of the current directory by name.
func (c *Controller) search() *tcell.EventKey {
	search := c.view.NewSearch()

	search.SetDoneFunc(func(key tcell.Key) {
		defer c.closeModal("modal")
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
	ordered := c.cur().ordered
	result := make([]string, 0, len(ordered))
	for _, word := range ordered {
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
		c.closeModal("modal")
		if buttonLabel != "ok" {
			return
		}
		c.deleteNode(n)
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(delQ, 44, 9), true, true)
	return nil
}

// deleteNode removes a key, or a whole subtree for a folder.
func (c *Controller) deleteNode(n *model.Node) {
	p := c.cur()
	runAsync(c, "deleting "+n.Name,
		func(ctx context.Context, _ func(string)) (struct{}, error) {
			if n.IsDir {
				return struct{}{}, p.model.DelDir(ctx, n.Key)
			}
			return struct{}{}, p.model.Del(ctx, n.Key)
		},
		func(_ struct{}, err error) {
			if err != nil {
				log.WithError(err).Error("delete failed")
				c.error("Error deleting key", err, false)
				return
			}
			c.view.Details.Clear()
			delete(p.stats, model.PrefixOf(n.Key))
			c.reloadPane(p, nil)
		})
}

func (c *Controller) create() *tcell.EventKey {
	createForm := c.view.NewCreateForm(fmt.Sprintf("Create key in: %s", c.cur().dir()))
	createForm.AddButton("Save", func() {
		name := createForm.GetFormItem(0).(*tview.InputField).GetText()
		value := createForm.GetFormItem(1).(*tview.InputField).GetText()
		isDir := createForm.GetFormItem(2).(*tview.Checkbox).IsChecked()
		c.closeModal("modal")
		c.createEntry(name, value, isDir)
	})
	createForm.AddButton("Quit", func() {
		c.closeModal("modal")
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(createForm, 60, 13), true, true)
	return nil
}

// createEntry creates a key or a folder below the current directory and moves
// the cursor onto it.
func (c *Controller) createEntry(name, value string, isDir bool) {
	name = strings.Trim(strings.TrimSpace(name), "/")
	if name == "" {
		c.error("Invalid name", errors.New("the key name must not be empty"), false)
		return
	}

	p := c.cur()
	full := model.ChildKey(p.prefix, name)
	runAsync(c, "creating "+model.PathOf(full),
		func(ctx context.Context, _ func(string)) (struct{}, error) {
			if isDir {
				return struct{}{}, p.model.MkDir(ctx, full)
			}
			return struct{}{}, p.model.Set(ctx, full, value)
		},
		func(_ struct{}, err error) {
			if err != nil {
				c.error("Error creating key", err, false)
				return
			}
			first, _, nested := strings.Cut(name, "/")
			c.reloadPane(p, func() {
				c.focusIn(p, displayName(first, isDir || nested))
			})
		})
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
	c.editValue(n)
	return nil
}

func (c *Controller) renameDir(n *model.Node) *tcell.EventKey {
	curBase := model.BaseOf(n.Key)
	form := c.view.NewEditValueForm(fmt.Sprintf("Rename folder: %s", n.Name), curBase)
	form.AddButton("Save", func() {
		newName := form.GetFormItem(0).(*tview.InputField).GetText()
		c.closeModal("modal")
		c.renameDirTo(n, newName)
	})
	form.AddButton("Quit", func() {
		c.closeModal("modal")
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(form, 60, 7), true, true)
	return nil
}

// renameDirTo renames a folder inside the current directory.
func (c *Controller) renameDirTo(n *model.Node, newName string) {
	newName = strings.TrimSpace(newName)
	if newName == "" || strings.Contains(newName, "/") {
		c.error("Invalid folder name",
			errors.New("the name must be non-empty and must not contain '/'"), false)
		return
	}
	if newName == model.BaseOf(n.Key) {
		return
	}

	p := c.cur()
	target := model.ChildKey(p.prefix, newName)
	runAsync(c, "renaming "+n.Name,
		func(ctx context.Context, _ func(string)) (struct{}, error) {
			return struct{}{}, p.model.RenameDir(ctx, n.Key, target)
		},
		func(_ struct{}, err error) {
			if err != nil {
				c.error("Failed to rename folder", err, false)
				return
			}
			delete(p.stats, model.PrefixOf(n.Key))
			c.reloadPane(p, func() {
				c.focusIn(p, displayName(newName, true))
			})
		})
}

// loadForEdit re-reads a key in full and refuses values that the editor would
// damage: the listing only holds a preview, so saving it back would truncate
// large values, and a TextArea cannot round-trip binary data.
func (c *Controller) loadForEdit(n *model.Node, done func(*model.Node)) {
	if err := editable(n); err != nil {
		c.error("Cannot edit "+n.Name, err, false)
		return
	}
	p := c.cur()
	runAsync(c, "reading "+n.Name,
		func(ctx context.Context, _ func(string)) (*model.Node, error) {
			return p.model.Get(ctx, n.Key)
		},
		func(full *model.Node, err error) {
			if err != nil {
				c.error("Failed to read "+n.Name, err, false)
				return
			}
			if err := editable(full); err != nil {
				c.error("Cannot edit "+n.Name, err, false)
				return
			}
			if !utf8.ValidString(full.Value) {
				c.error("Cannot edit "+n.Name,
					errors.New("the value is not valid UTF-8; editing it would corrupt binary data"), false)
				return
			}
			done(full)
		})
}

func (c *Controller) editValue(n *model.Node) {
	c.loadForEdit(n, func(full *model.Node) {
		ta := c.view.NewMultilineEditor(fmt.Sprintf(" Edit: %s ", full.Name), full.Value)
		ta.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
			switch ev.Key() {
			case tcell.KeyCtrlS:
				value := ta.GetText()
				if err := checkBeforeSave(full.Value, value); err != nil {
					c.view.CloseEditor()
					c.error("Not saved", err, false)
					return nil
				}
				c.view.CloseEditor()
				c.saveValue(full, value)
				return nil
			case tcell.KeyCtrlF:
				if pretty, err := format.PrettyJSON(ta.GetText()); err == nil {
					ta.SetText(pretty, true)
					c.setIdleStatus("value re-indented")
				} else {
					c.setIdleStatus("[red]not a JSON document[-]")
				}
				return nil
			case tcell.KeyEsc:
				c.view.CloseEditor()
				return nil
			}
			return ev
		})
		c.view.OpenEditor(ta)
	})
}

// checkBeforeSave refuses an edit that would store a broken document when the
// original value was valid JSON.
func checkBeforeSave(original, edited string) error {
	if !format.LooksLikeJSON(original) || format.ValidateJSON(original) != nil {
		return nil
	}
	if strings.TrimSpace(edited) == "" {
		return nil
	}
	if err := format.ValidateJSON(edited); err != nil {
		return fmt.Errorf("the value was valid JSON and the edit is not: %w", err)
	}
	return nil
}

// saveValue writes an edited value back and keeps the cursor on the key.
func (c *Controller) saveValue(n *model.Node, value string) {
	p := c.cur()
	runAsync(c, "saving "+n.Name,
		func(ctx context.Context, _ func(string)) (struct{}, error) {
			return struct{}{}, p.model.Set(ctx, n.Key, value)
		},
		func(_ struct{}, err error) {
			if err != nil {
				c.error("Failed to save value", err, false)
				return
			}
			c.reloadPane(p, func() {
				c.focusIn(p, displayName(model.BaseOf(n.Key), false))
			})
		})
}

// editExternally edits the selected value with $EDITOR.
func (c *Controller) editExternally() *tcell.EventKey {
	n, ok := c.selected()
	if !ok {
		return nil
	}
	c.loadForEdit(n, func(full *model.Node) {
		edited, changed, err := c.runEditor(full)
		if err != nil {
			c.error("Editor failed", err, false)
			return
		}
		if !changed {
			c.setIdleStatus("unchanged")
			return
		}
		if err := checkBeforeSave(full.Value, edited); err != nil {
			c.error("Not saved", err, false)
			return
		}
		c.saveValue(full, edited)
	})
	return nil
}

// editorCommand returns the command to run, honouring $VISUAL and $EDITOR.
func (c *Controller) editorCommand() []string {
	cmd := c.editor
	if cmd == "" {
		cmd = os.Getenv("VISUAL")
	}
	if cmd == "" {
		cmd = os.Getenv("EDITOR")
	}
	if strings.TrimSpace(cmd) == "" {
		cmd = "vi"
	}
	return strings.Fields(cmd)
}

// runEditor suspends the UI, hands the value to the external editor and reads
// the result back.
func (c *Controller) runEditor(n *model.Node) (string, bool, error) {
	f, err := os.CreateTemp("", "redis-walker-*.txt")
	if err != nil {
		return "", false, err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(n.Value); err != nil {
		f.Close()
		return "", false, err
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}

	argv := append(c.editorCommand(), path)
	var runErr error
	suspended := c.view.App.Suspend(func() {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		runErr = cmd.Run()
	})
	if !suspended {
		return "", false, errors.New("the terminal could not be released for the editor")
	}
	if runErr != nil {
		return "", false, fmt.Errorf("%s: %w", strings.Join(argv, " "), runErr)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	edited := string(data)
	return edited, edited != n.Value, nil
}

func (c *Controller) jump() *tcell.EventKey {
	inp := c.view.NewJump()
	inp.SetDoneFunc(func(key tcell.Key) {
		defer c.closeModal("modal")
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
	p := c.cur()
	isDirHint := strings.HasSuffix(raw, "/")
	target := strings.TrimSuffix(raw, "/")
	if !strings.HasPrefix(raw, "/") {
		target = p.prefix + target
	}

	runAsync(c, "resolving "+model.PathOf(target),
		func(ctx context.Context, _ func(string)) (*model.Node, error) {
			return p.model.Resolve(ctx, target)
		},
		func(nd *model.Node, err error) {
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
				p.prefix = model.PrefixOf(nd.Key)
				c.reloadPane(p, nil)
				return
			}
			p.prefix = model.ParentPrefix(model.PrefixOf(nd.Key))
			c.reloadPane(p, func() {
				c.focusIn(p, displayName(model.BaseOf(nd.Key), false))
			})
		})
}

// analyze walks the selected folder (or the current one) and reports how many
// keys it holds and where the memory goes.
func (c *Controller) analyze() *tcell.EventKey {
	p := c.cur()
	target := strings.TrimSuffix(p.prefix, "/")
	label := p.dir()
	if n, ok := c.selected(); ok && n.IsDir {
		target = n.Key
		label = n.Name
	}
	if target == "" && p.prefix == "" {
		label = "/"
	}

	runAsync(c, "analyzing "+label,
		func(ctx context.Context, report func(string)) (*model.DirStats, error) {
			progress := throttled(report, func(done int) string {
				return fmt.Sprintf("%d keys scanned", done)
			})
			return p.model.Stats(ctx, target, 12, progress)
		},
		func(st *model.DirStats, err error) {
			if err != nil {
				c.error("Analysis failed", err, false)
				return
			}
			p.stats[model.PrefixOf(target)] = st
			c.showReport(label, st)
			if n, ok := c.selected(); ok {
				c.fillDetails(mapKeyOf(n))
			}
		})
	return nil
}

// showReport displays the result of an analysis.
func (c *Controller) showReport(label string, st *model.DirStats) {
	body := statsSummary(st) + "\n"

	if len(st.TopPrefixes) > 0 {
		body += "[::b] Largest entries[::-]\n"
		for _, ps := range st.TopPrefixes {
			name := model.BaseOf(ps.Key)
			if ps.IsDir {
				name += "/"
			}
			body += fmt.Sprintf("   %-34s %10s  %d keys\n",
				tview.Escape(truncateMiddle(name, 34)), humanBytes(ps.Bytes), ps.Keys)
		}
		body += "\n"
	}
	if len(st.TopKeys) > 0 {
		body += "[::b] Largest keys[::-]\n"
		for _, ks := range st.TopKeys {
			body += fmt.Sprintf("   %-34s %10s  %s\n",
				tview.Escape(truncateMiddle(model.PathOf(ks.Key), 34)), humanBytes(ks.Bytes), ks.Type)
		}
	}

	report := c.view.NewReport(" Analysis: "+label+" ", body)
	report.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEsc, tcell.KeyEnter, tcell.KeyRune:
			c.closeModal("modal-report")
			return nil
		}
		return ev
	})
	c.view.Pages.AddPage("modal-report", c.view.ModalEdit(report, 78, 28), true, true)
	c.view.App.SetFocus(report)
}

// truncateMiddle shortens a long name so that both ends stay readable.
func truncateMiddle(s string, max int) string {
	if len([]rune(s)) <= max || max < 5 {
		return s
	}
	r := []rune(s)
	head := (max - 1) / 2
	tail := max - 1 - head
	return string(r[:head]) + "…" + string(r[len(r)-tail:])
}

// transfer copies or moves the selection to the other pane.
func (c *Controller) transfer(move bool) *tcell.EventKey {
	if !c.view.Dual() {
		c.setIdleStatus("[yellow]press F9 for the second pane first[-]")
		return nil
	}
	n, ok := c.selected()
	if !ok {
		return nil
	}

	src, dst := c.cur(), c.other()
	targetKey := model.ChildKey(dst.prefix, model.BaseOf(n.Key))
	verb := "Copy"
	if move {
		verb = "Move"
	}
	what := displayName(model.BaseOf(n.Key), n.IsDir)
	if n.IsDir {
		what += " (recursive)"
	}

	confirm := c.view.NewConfirm(fmt.Sprintf("%s %s\nto %s\non %s ?",
		verb, what, model.PathOf(targetKey), dst.model.Endpoint()))
	confirm.SetDoneFunc(func(_ int, label string) {
		c.closeModal("modal")
		if label != "ok" {
			return
		}
		c.runTransfer(src, dst, n, targetKey, move)
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(confirm, 60, 11), true, true)
	return nil
}

func (c *Controller) runTransfer(src, dst *pane, n *model.Node, targetKey string, move bool) {
	desc := "copying " + n.Name
	if move {
		desc = "moving " + n.Name
	}
	runAsync(c, desc,
		func(ctx context.Context, report func(string)) (struct{}, error) {
			progress := throttled(report, func(done int) string {
				return fmt.Sprintf("%d keys transferred", done)
			})
			switch {
			case n.IsDir && move:
				return struct{}{}, src.model.MoveDir(ctx, n.Key, dst.model, targetKey, false, progress)
			case n.IsDir:
				return struct{}{}, src.model.CopyDir(ctx, n.Key, dst.model, targetKey, false, progress)
			case move:
				return struct{}{}, src.model.MoveKey(ctx, n.Key, dst.model, targetKey, false)
			default:
				return struct{}{}, src.model.CopyKey(ctx, n.Key, dst.model, targetKey, false)
			}
		},
		func(_ struct{}, err error) {
			if err != nil {
				c.error("Transfer failed", err, false)
				return
			}
			c.reloadPane(dst, func() {
				c.focusIn(dst, displayName(model.BaseOf(targetKey), n.IsDir))
				if move {
					c.reloadPane(src, nil)
				}
			})
		})
}

// selectDatabase reconnects the active pane to another database.
func (c *Controller) selectDatabase() *tcell.EventKey {
	if c.connect == nil {
		c.setIdleStatus("[yellow]switching database is not available[-]")
		return nil
	}
	p := c.cur()
	inp := c.view.NewPrompt(" Database ", "database index", strconv.Itoa(p.model.DB()))
	inp.SetDoneFunc(func(key tcell.Key) {
		defer c.closeModal("modal")
		if key != tcell.KeyEnter {
			return
		}
		db, err := strconv.Atoi(strings.TrimSpace(inp.GetText()))
		if err != nil || db < 0 {
			c.error("Invalid database", fmt.Errorf("%q is not a database index", inp.GetText()), false)
			return
		}
		c.switchDatabase(p, db)
	})
	c.view.Pages.AddPage("modal", c.view.ModalEdit(inp, 40, 5), true, true)
	return nil
}

func (c *Controller) switchDatabase(p *pane, db int) {
	if p.model != nil && p.model.DB() == db {
		return
	}
	runAsync(c, fmt.Sprintf("connecting to database %d", db),
		func(ctx context.Context, _ func(string)) (*model.Model, error) {
			return c.connect(ctx, db)
		},
		func(m *model.Model, err error) {
			if err != nil {
				c.error("Failed to connect", err, false)
				return
			}
			old := p.model
			wasOwned := p.owned
			p.model = m
			p.owned = true
			p.prefix = ""
			p.stats = make(map[string]*model.DirStats)
			if wasOwned && old != nil && !c.modelInUse(old, p) {
				_ = old.Close()
			}
			c.reloadPane(p, nil)
		})
}

// modelInUse reports whether another pane still uses a connection.
func (c *Controller) modelInUse(m *model.Model, except *pane) bool {
	for _, p := range c.panes {
		if p != except && p.model == m {
			return true
		}
	}
	return false
}
