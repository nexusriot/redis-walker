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

func TestSetHeader(t *testing.T) {
	v := NewView()
	v.SetHeader("redis-walker test")
	if v.Header() != "redis-walker test" {
		t.Fatalf("Header() = %q", v.Header())
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
	help := helpText
	for _, key := range []string{"Ctrl+N", "Ctrl+E", "Ctrl+S", "Ctrl+J", "Ctrl+R", "Ctrl+Q", "Del", "F1", "Backspace"} {
		if !strings.Contains(help, key) {
			t.Errorf("the help modal does not document %s", key)
		}
		if !strings.Contains(keyHints, key) && key != "Backspace" && key != "Del" {
			t.Errorf("the hint line does not mention %s", key)
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
