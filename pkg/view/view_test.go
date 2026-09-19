package view

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestNewViewWiring(t *testing.T) {
	v := NewView()
	if v.App == nil || v.Frame == nil || v.Pages == nil || v.List == nil || v.Details == nil {
		t.Fatal("NewView left a primitive nil")
	}
	if name, _ := v.Pages.GetFrontPage(); name != "main" {
		t.Fatalf("front page = %q", name)
	}
	if v.ModalEdit(tview.NewBox(), 10, 10) == nil {
		t.Fatal("ModalEdit returned nil")
	}
}

func TestSetHeaderIsDrawn(t *testing.T) {
	v := NewView()
	v.SetHeader("redis-walker test")
	screen := tcell.NewSimulationScreen("UTF-8")
	v.App.SetScreen(screen)
	screen.SetSize(80, 24)
	v.App.ForceDraw()

	cells, w, h := screen.GetContents()
	var sb strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if r := cells[y*w+x].Runes; len(r) > 0 {
				sb.WriteRune(r[0])
			}
		}
	}
	if !strings.Contains(sb.String(), "redis-walker test") {
		t.Fatal("the header is not drawn")
	}
}

func TestCreateFormHasTheExpectedItems(t *testing.T) {
	v := NewView()
	f := v.NewCreateForm("title")
	if f.GetFormItemCount() != 3 {
		t.Fatalf("form has %d items, want 3 (name, value, is-dir)", f.GetFormItemCount())
	}
	if _, ok := f.GetFormItem(0).(*tview.InputField); !ok {
		t.Fatal("item 0 must be the key name input")
	}
	if _, ok := f.GetFormItem(2).(*tview.Checkbox); !ok {
		t.Fatal("item 2 must be the directory checkbox")
	}
}

func TestEditValueFormIsPrefilled(t *testing.T) {
	v := NewView()
	f := v.NewEditValueForm("title", "hello")
	if got := f.GetFormItem(0).(*tview.InputField).GetText(); got != "hello" {
		t.Fatalf("prefilled value = %q", got)
	}
}

func TestMultilineEditorKeepsTheValue(t *testing.T) {
	v := NewView()
	ta := v.NewMultilineEditor("t", "line1\nline2")
	if got := ta.GetText(); got != "line1\nline2" {
		t.Fatalf("editor text = %q", got)
	}
}

// The hint line and the help modal must mention every binding the controller
// installs, otherwise the documentation drifts away from the code.
func TestHotkeyDocumentationMentionsEveryBinding(t *testing.T) {
	for _, pairs := range [][][2]string{hintsTop, hintsBottom} {
		for _, h := range pairs {
			key := strings.Split(h[0], ",")[0]
			if !strings.Contains(helpText, key) {
				t.Errorf("the help modal does not document %s", key)
			}
		}
	}
	for _, key := range []string{"Ctrl+W", "Esc", "Ctrl+F"} {
		if !strings.Contains(helpText, key) {
			t.Errorf("the help modal does not document %s", key)
		}
	}
}

// Every key name must survive tview's tag parser, which reads "[F5]" and
// "[Enter]" as style tags unless they are escaped.
func TestHintsAreEscaped(t *testing.T) {
	rendered := renderHints(hintsTop) + " " + renderHints(hintsBottom)
	for _, pairs := range [][][2]string{hintsTop, hintsBottom} {
		for _, h := range pairs {
			want := "[" + h[0] + "]"
			escaped := "[" + h[0] + "[]"
			if !strings.Contains(rendered, want) && !strings.Contains(rendered, escaped) {
				t.Errorf("the hint for %s is neither literal nor escaped", h[0])
			}
		}
	}
}

func TestOpenAndCloseEditorRestoreTheRoot(t *testing.T) {
	v := NewView()
	screen := tcell.NewSimulationScreen("UTF-8")
	v.App.SetScreen(screen)

	ta := v.NewMultilineEditor("t", "x")
	v.OpenEditor(ta)
	if v.App.GetFocus() != ta {
		t.Fatal("the editor did not take focus")
	}
	v.CloseEditor()
	if v.App.GetFocus() != v.List {
		t.Fatal("closing the editor did not restore focus to the list")
	}
}
