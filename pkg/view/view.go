package view

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Version is the released version; overridden at build time with
// -ldflags "-X github.com/nexusriot/redis-walker/pkg/view.Version=vX.Y.Z".
var Version = "v0.0.3-dev"

// keyHints is the single-line cheat sheet drawn under the main frame.
// Words made only of letters look like tview colour tags, so "[Enter]",
// "[Backspace]" and "[Del]" must be escaped as "[Enter[]" - without the escape
// they are swallowed by the tag parser and never reach the screen.
const keyHints = "[::b][↓,↑][::-]Move [::b][Enter[][::-]Open [::b][Backspace[][::-]Up " +
	"[::b][Ctrl+N][::-]New [::b][Ctrl+E][::-]Edit [::b][Del[][::-]Delete " +
	"[::b][/,Ctrl+S][::-]Search [::b][Ctrl+J][::-]Jump [::b][Ctrl+R][::-]Refresh " +
	"[::b][F1,?][::-]Help [::b][Ctrl+Q][::-]Quit"

// View owns every tview primitive of the application.
type View struct {
	App       *tview.Application
	Frame     *tview.Frame
	Pages     *tview.Pages
	List      *tview.List
	Details   *tview.TextView
	ModalEdit func(p tview.Primitive, width, height int) tview.Primitive

	header string
}

// NewView builds the main layout.
func NewView() *View {
	app := tview.NewApplication()

	list := tview.NewList().ShowSecondaryText(false)
	list.SetBorder(true).SetTitleAlign(tview.AlignLeft)
	list.SetSelectedTextColor(tcell.ColorBlack).
		SetSelectedBackgroundColor(tcell.ColorYellow)

	tv := tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true).
		SetChangedFunc(func() {
			app.Draw()
		})
	tv.SetBorder(true).SetTitle("Details")

	main := tview.NewFlex()
	main.AddItem(list, 0, 2, true)
	main.AddItem(tv, 0, 3, false)

	pages := tview.NewPages().AddPage("main", main, true, true)

	modal := func(p tview.Primitive, width, height int) tview.Primitive {
		return tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(nil, 0, 1, false).
				AddItem(p, height, 1, true).
				AddItem(nil, 0, 1, false), width, 1, true).
			AddItem(nil, 0, 1, false)
	}

	frame := tview.NewFrame(pages)
	frame.AddText(keyHints, false, tview.AlignCenter, tcell.ColorWhite)

	app.SetRoot(frame, true)

	return &View{
		App:       app,
		Frame:     frame,
		Pages:     pages,
		List:      list,
		Details:   tv,
		ModalEdit: modal,
	}
}

// SetHeader sets the centred title line at the top of the frame.
func (v *View) SetHeader(text string) {
	v.header = text
	v.Frame.AddText(text, true, tview.AlignCenter, tcell.ColorGreen)
}

// Header returns the text passed to SetHeader.
func (v *View) Header() string { return v.header }

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

	// Esc closes the modal.
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

func (v *View) NewSearch() *tview.InputField {
	search := tview.NewInputField().
		SetPlaceholder("name of a key or folder in this directory").
		SetFieldTextColor(tcell.ColorWhite)
	search.SetBorder(true).SetTitle(" Search ")
	return search
}

func (v *View) NewJump() *tview.InputField {
	inp := tview.NewInputField().
		SetPlaceholder("Jump to key or dir/ (absolute or relative)")
	inp.SetBorder(true).SetTitle(" Jump ")
	return inp
}

func (v *View) NewDeleteQ(header string) *tview.Modal {
	deleteQ := tview.NewModal()
	deleteQ.SetText("Delete " + header + " ?").AddButtons([]string{"ok", "cancel"})
	return deleteQ
}

func (v *View) NewErrorMessageQ(header string, details string) *tview.Modal {
	errorQ := tview.NewModal()
	errorQ.SetText(header + ":\n" + details).
		SetBackgroundColor(tcell.ColorRed).
		AddButtons([]string{"ok"})
	return errorQ
}

const helpText = `
  [::b]Navigation[::-]
    Enter         Open folder
    Backspace     Up to the parent folder
    Ctrl+J        Jump to a key or folder (a folder ends with '/')
    Ctrl+R        Reload the current folder

  [::b]Actions[::-]
    Ctrl+N        Create a key or folder
    Ctrl+E        Edit a value / rename a folder
    Del           Delete (recursive for folders)

  [::b]Search[::-]
    /, Ctrl+S     Find an entry in the current folder

  [::b]Editor[::-]
    Ctrl+S        Save
    Esc           Cancel

  [::b]Misc[::-]
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
		SetTitle(title + " [Ctrl+S=Save | Esc=Cancel]")
	return ta
}

// OpenEditor replaces the root with a full screen editor.
func (v *View) OpenEditor(p tview.Primitive) {
	editor := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(p, 0, 1, true)
	v.App.SetRoot(editor, true)
	v.App.SetFocus(p)
}

// CloseEditor restores the main layout.
func (v *View) CloseEditor() {
	v.App.SetRoot(v.Frame, true)
	v.App.SetFocus(v.List)
}
