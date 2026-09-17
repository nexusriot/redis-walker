package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/rivo/tview"

	"github.com/nexusriot/redis-walker/pkg/model"
)

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"line\nbreak": "line\nbreak",
		"a\x00b":      `a\x00b`,
		"a\x1bb":      `a\x1bb`,
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
	// Regression: a value containing tview colour tags used to be rendered as
	// colours instead of text.
	if got := sanitize("[red]danger[-]"); got == "[red]danger[-]" {
		t.Errorf("colour tags were not escaped: %q", got)
	}
}

func TestEditable(t *testing.T) {
	if err := editable(&model.Node{IsDir: true}); err == nil {
		t.Error("directories must not be editable")
	}
	if err := editable(&model.Node{Type: "hash"}); err == nil {
		t.Error("hashes must not be editable")
	}
	if err := editable(&model.Node{Type: model.TypeString}); err != nil {
		t.Errorf("strings must be editable: %v", err)
	}
}

func TestHumanTTL(t *testing.T) {
	if got := humanTTL(-1); got != "none" {
		t.Errorf("humanTTL(-1) = %q", got)
	}
	if got := humanTTL(90 * time.Second); got != "1m30s" {
		t.Errorf("humanTTL(90s) = %q", got)
	}
}

func TestListShowsDirsFirstThenKeys(t *testing.T) {
	h := newHarness(t, map[string]string{
		"zeta":    "1",
		"alpha/x": "1",
		"beta":    "1",
		"gamma/y": "1",
		"user:1":  "1",
	})
	got := h.items()
	want := []string{"[..]", "📁 alpha/", "📁 gamma/", "   beta", "   user:1", "   zeta"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("list = %v, want %v", got, want)
	}
}

// Regression: the search dialog sorted by display name while the list sorted by
// an internal map key, so "jump to name" landed on the wrong row whenever one
// name was a prefix of another.
func TestSearchOrderMatchesListOrder(t *testing.T) {
	h := newHarness(t, map[string]string{
		"a/k":   "1",
		"aZ/k":  "1",
		"a-b/k": "1",
	})
	h.do(func() { h.c.focus("aZ/") })
	if got := h.currentItem(); got != "📁 aZ/" {
		t.Fatalf("focus landed on %q, want the aZ folder", got)
	}
	h.do(func() { h.c.focus("a/") })
	if got := h.currentItem(); got != "📁 a/" {
		t.Fatalf("focus landed on %q, want the a folder", got)
	}
}

func TestNavigationDownAndUp(t *testing.T) {
	h := newHarness(t, map[string]string{
		"app/cfg/db":  "1",
		"app/cfg/web": "2",
		"other":       "3",
	})

	h.do(func() { h.c.JumpTo("app/") })
	if got := h.c.CurrentPrefix(); got != "app/" {
		t.Fatalf("prefix = %q", got)
	}
	if got := h.items(); len(got) != 2 || got[1] != "📁 cfg/" {
		t.Fatalf("items = %v", got)
	}

	h.do(func() { h.c.Up() })
	if got := h.c.CurrentPrefix(); got != "" {
		t.Fatalf("prefix after Up = %q", got)
	}
	// The cursor must land back on the folder we came from.
	if got := h.currentItem(); got != "📁 app/" {
		t.Fatalf("cursor = %q, want the app folder", got)
	}

	// Up at the root is a no-op.
	h.do(func() { h.c.Up() })
	if got := h.c.CurrentPrefix(); got != "" {
		t.Fatalf("prefix = %q", got)
	}
}

// Regression: keys without a leading slash used to be shown under a fabricated
// path, so editing them created a second key.
func TestEditKeyWithoutLeadingSlash(t *testing.T) {
	h := newHarness(t, map[string]string{"session:42": "old"})

	var n *model.Node
	h.do(func() {
		h.v.List.SetCurrentItem(1)
		var ok bool
		n, ok = h.c.selected()
		if !ok {
			t.Error("nothing selected")
		}
	})
	if n == nil || n.Key != "session:42" {
		t.Fatalf("selected node = %+v", n)
	}

	h.do(func() {
		if err := h.c.saveValue(n, "new"); err != nil {
			t.Errorf("saveValue: %v", err)
		}
	})
	if v, _ := h.mr.Get("session:42"); v != "new" {
		t.Fatalf("session:42 = %q, want the edited value", v)
	}
	if h.mr.Exists("/session:42") {
		t.Fatal("a duplicate key with a leading slash was created")
	}
}

// Regression: the editor used the truncated listing preview, so saving a large
// value silently cut it down to the preview size.
func TestEditorLoadsFullValueNotPreview(t *testing.T) {
	big := strings.Repeat("x", 1000)
	h := newHarness(t, map[string]string{"big": big}, func(o *model.Options) {
		o.PreviewBytes = 16
	})

	var full *model.Node
	h.do(func() {
		h.v.List.SetCurrentItem(1)
		n, _ := h.c.selected()
		if !n.Truncated {
			t.Error("the listing should hold a truncated preview")
		}
		var err error
		full, err = h.c.loadForEdit(n)
		if err != nil {
			t.Errorf("loadForEdit: %v", err)
		}
	})
	if full == nil || len(full.Value) != len(big) {
		t.Fatalf("the editor received %d bytes, want %d", len(full.Value), len(big))
	}
}

func TestEditorRefusesNonStringAndBinary(t *testing.T) {
	h := newHarness(t, map[string]string{"bin": "a\xffb"})
	h.mr.HSet("h", "f", "v")
	h.do(func() { h.c.updateList() })

	h.do(func() {
		for mk, n := range h.c.currentNodes {
			_ = mk
			if _, err := h.c.loadForEdit(n); err == nil {
				t.Errorf("loadForEdit(%q) must fail", n.Key)
			}
		}
	})
}

func TestCreateKeyAndFolder(t *testing.T) {
	h := newHarness(t, nil)

	h.do(func() {
		if err := h.c.createEntry("cfg", "v", false); err != nil {
			t.Errorf("createEntry: %v", err)
		}
	})
	if v, _ := h.mr.Get("cfg"); v != "v" {
		t.Fatalf("cfg = %q", v)
	}
	if got := h.currentItem(); got != "   cfg" {
		t.Fatalf("cursor = %q", got)
	}

	h.do(func() {
		if err := h.c.createEntry("dir", "", true); err != nil {
			t.Errorf("createEntry: %v", err)
		}
	})
	if !h.mr.Exists("dir/.dir") {
		t.Fatal("the folder marker was not created")
	}
	if got := h.currentItem(); got != "📁 dir/" {
		t.Fatalf("cursor = %q", got)
	}
}

func TestCreateInsideFolderUsesRealPrefix(t *testing.T) {
	h := newHarness(t, map[string]string{"app/a": "1"})
	h.do(func() { h.c.JumpTo("app/") })
	h.do(func() {
		if err := h.c.createEntry("b", "2", false); err != nil {
			t.Errorf("createEntry: %v", err)
		}
	})
	if v, _ := h.mr.Get("app/b"); v != "2" {
		t.Fatalf("app/b = %q", v)
	}
}

func TestCreateRejectsEmptyName(t *testing.T) {
	h := newHarness(t, nil)
	h.do(func() {
		for _, name := range []string{"", "   ", "///"} {
			if err := h.c.createEntry(name, "v", false); err == nil {
				t.Errorf("createEntry(%q) must fail", name)
			}
		}
	})
	if keys := h.mr.Keys(); len(keys) != 0 {
		t.Fatalf("keys were created: %v", keys)
	}
}

func TestCreateNestedNameShowsAsFolder(t *testing.T) {
	h := newHarness(t, nil)
	h.do(func() {
		if err := h.c.createEntry("a/b", "v", false); err != nil {
			t.Errorf("createEntry: %v", err)
		}
	})
	if v, _ := h.mr.Get("a/b"); v != "v" {
		t.Fatalf("a/b = %q", v)
	}
	if got := h.currentItem(); got != "📁 a/" {
		t.Fatalf("cursor = %q, want the new folder", got)
	}
}

func TestDeleteKeyAndFolder(t *testing.T) {
	h := newHarness(t, map[string]string{
		"keep":   "1",
		"gone":   "1",
		"tree/a": "1",
		"tree/b": "1",
	})

	h.do(func() {
		n := h.c.currentNodes["gone|file"]
		if err := h.c.deleteNode(n); err != nil {
			t.Errorf("deleteNode: %v", err)
		}
	})
	if h.mr.Exists("gone") {
		t.Fatal("the key was not deleted")
	}

	h.do(func() {
		n := h.c.currentNodes["tree|dir"]
		if err := h.c.deleteNode(n); err != nil {
			t.Errorf("deleteNode: %v", err)
		}
	})
	if h.mr.Exists("tree/a") || h.mr.Exists("tree/b") {
		t.Fatal("the folder was not deleted recursively")
	}
	if !h.mr.Exists("keep") {
		t.Fatal("an unrelated key was deleted")
	}
}

func TestRenameFolder(t *testing.T) {
	h := newHarness(t, map[string]string{"old/a": "1", "old/b/c": "2"})

	h.do(func() {
		n := h.c.currentNodes["old|dir"]
		if err := h.c.renameDirTo(n, "new"); err != nil {
			t.Errorf("renameDirTo: %v", err)
		}
	})
	if !h.mr.Exists("new/a") || !h.mr.Exists("new/b/c") || h.mr.Exists("old/a") {
		t.Fatalf("keys after rename: %v", h.mr.Keys())
	}
	if got := h.currentItem(); got != "📁 new/" {
		t.Fatalf("cursor = %q", got)
	}
}

func TestRenameFolderRejectsBadNames(t *testing.T) {
	h := newHarness(t, map[string]string{"old/a": "1"})
	h.do(func() {
		n := h.c.currentNodes["old|dir"]
		for _, name := range []string{"", "  ", "a/b"} {
			if err := h.c.renameDirTo(n, name); err == nil {
				t.Errorf("renameDirTo(%q) must fail", name)
			}
		}
		if err := h.c.renameDirTo(n, "old"); err != nil {
			t.Errorf("renaming to the same name must be a no-op: %v", err)
		}
	})
	if !h.mr.Exists("old/a") {
		t.Fatal("the folder was damaged by a rejected rename")
	}
}

func TestJumpToKeyMovesCursor(t *testing.T) {
	h := newHarness(t, map[string]string{"a/b/c": "1", "a/b/d": "2"})
	h.do(func() { h.c.JumpTo("/a/b/d") })
	if got := h.c.CurrentPrefix(); got != "a/b/" {
		t.Fatalf("prefix = %q", got)
	}
	if got := h.currentItem(); got != "   d" {
		t.Fatalf("cursor = %q", got)
	}
}

func TestJumpToMissingKeyShowsError(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.do(func() { h.c.JumpTo("/nope") })
	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q, want the error modal", got)
	}
	if !strings.Contains(h.screenText(), "Not found") {
		t.Fatal("the error modal does not show 'Not found'")
	}
}

func TestJumpDirHintRejectsAKey(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.do(func() { h.c.JumpTo("/a/") })
	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q", got)
	}
}

func TestDetailsShowTypeSizeAndTTL(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "hello"})
	h.mr.SetTTL("k", time.Minute)
	h.mr.HSet("h", "f", "v")
	h.do(func() { h.c.updateList() })

	h.do(func() { h.c.focus("k") })
	d := h.details()
	for _, want := range []string{"/k", "string", "5 bytes", "hello"} {
		if !strings.Contains(d, want) {
			t.Fatalf("details %q do not contain %q", d, want)
		}
	}

	h.do(func() { h.c.focus("h") })
	d = h.details()
	if !strings.Contains(d, "hash") || !strings.Contains(d, "not displayed") {
		t.Fatalf("details for a hash = %q", d)
	}
}

// Regression: a value containing tview markup must be shown literally.
func TestDetailsDoNotInterpretColourTags(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "[red]boom[-]"})
	h.do(func() { h.c.focus("k") })
	if !strings.Contains(h.screenText(), "[red]boom") {
		t.Fatalf("colour tags were interpreted instead of shown:\n%s", h.details())
	}
}

func TestTruncatedListingIsFlaggedInTheTitle(t *testing.T) {
	seed := map[string]string{}
	for i := 0; i < 10; i++ {
		seed[string(rune('a'+i))] = "1"
	}
	h := newHarness(t, seed, func(o *model.Options) { o.MaxKeys = 3 })
	if !strings.Contains(h.screenText(), "truncated") {
		t.Fatal("a truncated listing must be visible in the title")
	}
}

// Regression: when a listing failed, the previous directory's entries stayed on
// screen under the new directory's title.
func TestListingErrorClearsStaleNodes(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1", "b": "2"})
	h.mr.Close() // the server goes away

	h.do(func() { h.c.updateList() })
	if got := len(h.c.currentNodes); got != 0 {
		t.Fatalf("%d stale nodes survived a failed listing", got)
	}
	if got := h.items(); len(got) != 1 || got[0] != "[..]" {
		t.Fatalf("list = %v, want only the parent entry", got)
	}
	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q, want the error modal", got)
	}
}

func TestSelectedIgnoresParentEntry(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.do(func() {
		h.v.List.SetCurrentItem(0)
		if _, ok := h.c.selected(); ok {
			t.Error("the '..' entry must not resolve to a node")
		}
	})
}

func TestKeyBindingOpensAndClosesHelp(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.key(tcell.KeyF1, 0)
	h.waitFor("the help modal", func() bool {
		name, _ := h.v.Pages.GetFrontPage()
		return name == "modal-help"
	})
	if !strings.Contains(h.screenText(), "Hotkeys") {
		t.Fatal("the help modal is not rendered")
	}
	h.key(tcell.KeyEsc, 0)
	h.waitFor("the help modal to close", func() bool {
		name, _ := h.v.Pages.GetFrontPage()
		return name == "main"
	})
}

func TestKeyBindingBackspaceGoesUp(t *testing.T) {
	h := newHarness(t, map[string]string{"app/cfg": "1"})
	h.do(func() { h.c.JumpTo("app/") })
	h.key(tcell.KeyBackspace2, 0)
	h.waitFor("the root listing", func() bool { return h.c.CurrentPrefix() == "" })
}

func TestKeyBindingRefresh(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.mr.Set("b", "2")
	h.key(tcell.KeyCtrlR, 0)
	h.waitFor("the new key to appear", func() bool { return len(h.c.currentNodes) == 2 })
}

func TestKeyBindingCtrlQStops(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.key(tcell.KeyCtrlQ, 0)
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
		h.done <- nil // let the cleanup hook find the result
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+Q did not stop the application")
	}
}

func TestMatchOrdered(t *testing.T) {
	h := newHarness(t, map[string]string{
		"App/x":  "1",
		"apple":  "1",
		"banana": "1",
	})
	var got []string
	h.do(func() { got = h.c.matchOrdered(" ap ") })
	if len(got) != 2 || got[0] != "App/" || got[1] != "apple" {
		t.Fatalf("matchOrdered = %v, want the folder first then the key", got)
	}
	h.do(func() { got = h.c.matchOrdered("  ") })
	if got != nil {
		t.Fatalf("matchOrdered(blank) = %v, want nil", got)
	}
}

func TestDialogsOpenAndClose(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1", "dir/x": "1"})

	for _, tc := range []struct {
		name string
		open func()
	}{
		{"create", func() { h.c.create() }},
		{"search", func() { h.c.search() }},
		{"jump", func() { h.c.jump() }},
		{"delete", func() { h.v.List.SetCurrentItem(2); h.c.delete() }},
		{"rename", func() { h.v.List.SetCurrentItem(1); h.c.editSelected() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.do(tc.open)
			if got := h.frontPage(); got != "modal" {
				t.Fatalf("%s did not open a modal (front page %q)", tc.name, got)
			}
			h.do(func() { h.v.Pages.RemovePage("modal") })
		})
	}
}

func TestDeleteOnTheParentEntryDoesNothing(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.do(func() {
		h.v.List.SetCurrentItem(0)
		h.c.delete()
	})
	if got := h.frontPage(); got != "main" {
		t.Fatalf("front page = %q, want no modal", got)
	}
	if !h.mr.Exists("a") {
		t.Fatal("a key was deleted")
	}
}

func TestEditSelectedOpensTheEditorForStrings(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "value"})
	h.do(func() {
		h.v.List.SetCurrentItem(1)
		h.c.editSelected()
	})
	var text string
	h.do(func() {
		ta, ok := h.v.App.GetFocus().(*tview.TextArea)
		if !ok {
			t.Errorf("focus is %T, want the multiline editor", h.v.App.GetFocus())
			return
		}
		text = ta.GetText()
	})
	if text != "value" {
		t.Fatalf("editor text = %q", text)
	}
	h.do(func() { h.v.CloseEditor() })
}

func TestEditSelectedRefusesHashes(t *testing.T) {
	h := newHarness(t, nil)
	h.mr.HSet("h", "f", "v")
	h.do(func() {
		h.c.updateList()
		h.v.List.SetCurrentItem(1)
		h.c.editSelected()
	})
	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q, want the error modal", got)
	}
	if !strings.Contains(h.screenText(), "hash") {
		t.Fatal("the error does not name the offending type")
	}
}

// Regression: bracketed key names made of letters ("[Enter]", "[Backspace]",
// "[Del]") look like tview colour tags and disappeared from the hint line.
func TestHintLineRendersKeyNames(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	screen := h.screenText()
	for _, want := range []string{"[Enter]", "[Backspace]", "[Del]", "[Ctrl+N]", "[Ctrl+Q]"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the hint line does not show %s", want)
		}
	}
}

// A key name that looks like a tview tag must be shown literally in the list.
func TestListLabelsEscapeMarkup(t *testing.T) {
	h := newHarness(t, map[string]string{"[red]name": "1"})
	if !strings.Contains(h.screenText(), "[red]name") {
		t.Fatalf("the key name was interpreted as markup:\n%s", h.screenText())
	}
}
