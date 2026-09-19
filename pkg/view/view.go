package view

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Version is the released version; overridden at build time with
// -ldflags "-X github.com/nexusriot/redis-walker/pkg/view.Version=vX.Y.Z".
var Version = "v0.2.0"

// hintsTop and hintsBottom are the cheat sheet drawn under the main frame.
// They are rendered through tview.Escape because tview reads any bracketed word
// of letters and digits - "[Enter]", "[Del]", "[F5]" - as a style tag and would
// otherwise swallow it.
var (
	hintsTop = [][2]string{
		{"Enter", "Open"}, {"Backspace", "Up"}, {"Ctrl+N", "New"}, {"Ctrl+E", "Edit"},
		{"Ctrl+O", "Editor"}, {"Del", "Delete"}, {"Ctrl+S", "Search"}, {"Ctrl+J", "Jump"},
	}
	hintsBottom = [][2]string{
		{"Ctrl+A", "Analyze"}, {"Ctrl+V", "View"}, {"Ctrl+R", "Reload"}, {"F5", "Copy"},
		{"F6", "Move"}, {"F9", "Panes"}, {"Tab", "Pane"}, {"Ctrl+D", "DB"},
		{"F1,?", "Help"}, {"Ctrl+Q", "Quit"},
	}
)

// renderHints turns key/action pairs into a single line of markup.
func renderHints(hints [][2]string) string {
	parts := make([]string, 0, len(hints))
	for _, h := range hints {
		parts = append(parts, "[::b]"+tview.Escape("["+h[0]+"]")+"[::-]"+h[1])
	}
	return strings.Join(parts, " ")
}

// View owns every tview primitive of the application.
type View struct {
	App     *tview.Application
	Root    *tview.Flex
	Frame   *tview.Frame
	Pages   *tview.Pages
	Details *tview.TextView
	Status  *tview.TextView

	// Lists holds the two browser panes; Lists[1] is only shown in dual mode.
	Lists [2]*tview.List
	// List is the pane the user is working in.
	List *tview.List

	ModalEdit func(p tview.Primitive, width, height int) tview.Primitive

	body   *tview.Flex
	dual   bool
	active int
}

// NewView builds the main layout.
func NewView() *View {
	app := tview.NewApplication()

	v := &View{App: app}

	for i := range v.Lists {
		list := tview.NewList().ShowSecondaryText(false)
		list.SetBorder(true).SetTitleAlign(tview.AlignLeft)
		list.SetSelectedTextColor(tcell.ColorBlack).
			SetSelectedBackgroundColor(tcell.ColorYellow)
		v.Lists[i] = list
	}
	v.List = v.Lists[0]

	v.Details = tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	v.Details.SetBorder(true).SetTitle("Details")

	v.Status = tview.NewTextView().SetDynamicColors(true)

	v.body = tview.NewFlex()
	v.Pages = tview.NewPages().AddPage("main", v.body, true, true)

	v.ModalEdit = func(p tview.Primitive, width, height int) tview.Primitive {
		return tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(nil, 0, 1, false).
				AddItem(p, height, 1, true).
				AddItem(nil, 0, 1, false), width, 1, true).
			AddItem(nil, 0, 1, false)
	}

	v.Frame = tview.NewFrame(v.Pages)
	v.Frame.AddText(renderHints(hintsTop), false, tview.AlignCenter, tcell.ColorWhite)
	v.Frame.AddText(renderHints(hintsBottom), false, tview.AlignCenter, tcell.ColorWhite)

	v.Root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.Frame, 0, 1, true).
		AddItem(v.Status, 1, 0, false)

	v.layout()
	v.markActive()
	app.SetRoot(v.Root, true)

	return v
}

// layout rebuilds the main area for the current pane mode.
func (v *View) layout() {
	v.body.Clear()
	if !v.dual {
		v.body.SetDirection(tview.FlexColumn).
			AddItem(v.Lists[0], 0, 2, true).
			AddItem(v.Details, 0, 3, false)
		return
	}
	panes := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(v.Lists[0], 0, 1, true).
		AddItem(v.Lists[1], 0, 1, false)
	v.body.SetDirection(tview.FlexRow).
		AddItem(panes, 0, 3, true).
		AddItem(v.Details, 0, 1, false)
}

// SetDual shows or hides the second browser pane.
func (v *View) SetDual(dual bool) {
	if v.dual == dual {
		return
	}
	v.dual = dual
	if !dual && v.active == 1 {
		v.SetActive(0)
	}
	v.layout()
	v.markActive()
	v.App.SetFocus(v.List)
}

// Dual reports whether the second pane is shown.
func (v *View) Dual() bool { return v.dual }

// SetActive selects the pane the user works in.
func (v *View) SetActive(i int) {
	if i < 0 || i >= len(v.Lists) || (i == 1 && !v.dual) {
		return
	}
	v.active = i
	v.List = v.Lists[i]
	v.markActive()
	v.App.SetFocus(v.List)
}

func (v *View) markActive() {
	for i, list := range v.Lists {
		if i == v.active && v.dual {
			list.SetBorderColor(tcell.ColorYellow)
			continue
		}
		list.SetBorderColor(tcell.ColorWhite)
	}
}

// SetStatus writes the bottom status line.
func (v *View) SetStatus(text string) { v.Status.SetText(text) }

// SetHeader sets the centred title line at the top of the frame.
func (v *View) SetHeader(text string) {
	v.Frame.AddText(text, true, tview.AlignCenter, tcell.ColorGreen)
}

func (v *View) NewCreateForm(header string) *tview.Form {
	form := tview.NewForm().
		AddInputField("Key name", "", 40, nil, nil).
		AddInputField("Value", "", 40, nil, nil)

	form.AddCheckbox("Is a Directory", false, func(checked bool) {})

	form.SetBorder(true).
		SetTitle(header).
		SetTitleAlign(tview.AlignLeft)
	form.SetBorderPadding(1, 1, 2, 2)
	form.SetLabelColor(tcell.ColorYellow)
	form.SetFieldTextColor(tcell.ColorWhite)
	form.SetFieldBackgroundColor(tcell.ColorDefault)
	form.SetButtonsAlign(tview.AlignCenter)

	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			v.Pages.RemovePage("modal")
			v.App.SetFocus(v.List)
			return nil
		}
		return event
	})

	return form
}

func (v *View) NewEditValueForm(header string, value string) *tview.Form {
	form := tview.NewForm().AddInputField("Value", "", 60, nil, nil)
	form.GetFormItem(0).(*tview.InputField).SetText(value)
	form.SetBorder(true)
	form.SetTitle(header)
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			v.Pages.RemovePage("modal")
			v.App.SetFocus(v.List)
			return nil
		}
		return event
	})
	return form
}

// NewPrompt is the single line input used by search, jump and the database
// picker.
func (v *View) NewPrompt(title, placeholder, value string) *tview.InputField {
	inp := tview.NewInputField().
		SetPlaceholder(placeholder).
		SetFieldTextColor(tcell.ColorWhite)
	inp.SetText(value)
	inp.SetBorder(true).SetTitle(title)
	return inp
}

func (v *View) NewSearch() *tview.InputField {
	return v.NewPrompt(" Search ", "name of a key or folder in this directory", "")
}

func (v *View) NewJump() *tview.InputField {
	return v.NewPrompt(" Jump ", "Jump to key or dir/ (absolute or relative)", "")
}

// NewConfirm is the yes/no dialog used before a destructive action.
func (v *View) NewConfirm(text string) *tview.Modal {
	m := tview.NewModal()
	m.SetText(tview.Escape(text)).AddButtons([]string{"ok", "cancel"})
	return m
}

func (v *View) NewDeleteQ(header string) *tview.Modal {
	return v.NewConfirm("Delete " + header + " ?")
}

func (v *View) NewErrorMessageQ(header string, details string) *tview.Modal {
	errorQ := tview.NewModal()
	errorQ.SetText(tview.Escape(header + ":\n" + details)).
		SetBackgroundColor(tcell.ColorRed).
		AddButtons([]string{"ok"})
	return errorQ
}

// NewReport is a scrollable text window used for the folder analysis.
func (v *View) NewReport(title, body string) *tview.TextView {
	tv := tview.NewTextView()
	tv.SetDynamicColors(true)
	tv.SetWordWrap(false)
	tv.SetScrollable(true)
	tv.SetText(body)
	tv.SetBorder(true)
	tv.SetTitle(title)
	return tv
}

const helpText = `
  [::b]Navigation[::-]
    Enter         Open folder
    Backspace     Up to the parent folder
    Ctrl+J        Jump to a key or folder (a folder ends with '/')
    Ctrl+R        Reload the current folder
    /, Ctrl+S     Find an entry in the current folder

  [::b]Panes[::-]
    F9, Ctrl+W    Show or hide the second pane
    Tab           Switch to the other pane
    F5            Copy the selection to the other pane
    F6            Move the selection to the other pane
    Ctrl+D        Connect the active pane to another database

  [::b]Actions[::-]
    Ctrl+N        Create a key or folder
    Ctrl+E        Edit a value / rename a folder
    Ctrl+O        Edit the value in $EDITOR
    Del           Delete (recursive for folders)
    Ctrl+A        Analyze a folder (keys, memory, biggest prefixes)
    Ctrl+V        Switch the value view (decoded / raw / hex)

  [::b]Editor[::-]
    Ctrl+S        Save
    Ctrl+F        Re-indent a JSON value
    Esc           Cancel

  [::b]Misc[::-]
    Esc           Cancel the running operation
    F1 or ?       This help
    Ctrl+Q        Quit

  [dim]Press any key to close.[-]
`

func (v *View) NewHotkeysModal() *tview.TextView {
	tv := tview.NewTextView()
	tv.SetDynamicColors(true)
	tv.SetTextAlign(tview.AlignLeft)
	tv.SetWordWrap(true)
	tv.SetText(helpText)
	tv.SetBorder(true)
	tv.SetTitle(" Hotkeys ")
	return tv
}

func (v *View) NewMultilineEditor(title, initial string) *tview.TextArea {
	ta := tview.NewTextArea().
		SetText(initial, false).
		SetPlaceholder("")
	ta.SetBorder(true).
		SetTitle(title + " [Ctrl+S=Save | Ctrl+F=Format | Esc=Cancel]")
	return ta
}

// OpenEditor replaces the root with a full screen editor.
func (v *View) OpenEditor(p tview.Primitive) {
	editor := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(p, 0, 1, true).
		AddItem(v.Status, 1, 0, false)
	v.App.SetRoot(editor, true)
	v.App.SetFocus(p)
}

// CloseEditor restores the main layout.
func (v *View) CloseEditor() {
	v.Root.Clear().
		AddItem(v.Frame, 0, 1, true).
		AddItem(v.Status, 1, 0, false)
	v.App.SetRoot(v.Root, true)
	v.App.SetFocus(v.List)
}
