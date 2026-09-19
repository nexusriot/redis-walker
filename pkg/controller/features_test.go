package controller

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/nexusriot/redis-walker/pkg/model"
)

func TestOperationsRunInTheBackground(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})

	started := make(chan struct{})
	release := make(chan struct{})
	h.do(func() {
		runAsync(h.c, "slow work",
			func(ctx context.Context, report func(string)) (int, error) {
				close(started)
				<-release
				report("half way")
				return 42, nil
			},
			func(int, error) {})
	})

	<-started
	// The UI keeps answering while the operation runs.
	if h.c.Idle() {
		t.Fatal("the controller should report itself busy")
	}
	if got := h.status(); !strings.Contains(got, "slow work") {
		t.Fatalf("status = %q, want the running operation", got)
	}
	if !strings.Contains(h.status(), "Esc to cancel") {
		t.Fatalf("status = %q, want the cancel hint", h.status())
	}
	close(release)
	h.waitIdle()
}

func TestOnlyOneOperationRunsAtATime(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})

	release := make(chan struct{})
	second := false
	h.do(func() {
		runAsync(h.c, "first",
			func(ctx context.Context, _ func(string)) (int, error) { <-release; return 1, nil },
			func(int, error) {})
	})
	h.do(func() {
		second = runAsync(h.c, "second",
			func(ctx context.Context, _ func(string)) (int, error) { return 2, nil },
			func(int, error) {})
	})
	if second {
		t.Fatal("a second operation must not start while one is running")
	}
	if got := h.status(); !strings.Contains(got, "busy") {
		t.Fatalf("status = %q, want a busy message", got)
	}
	close(release)
	h.waitIdle()
}

func TestEscapeCancelsTheRunningOperation(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})

	cancelled := make(chan error, 1)
	h.do(func() {
		runAsync(h.c, "cancellable",
			func(ctx context.Context, _ func(string)) (int, error) {
				<-ctx.Done()
				cancelled <- ctx.Err()
				return 0, ctx.Err()
			},
			func(int, error) { t.Error("the done callback must not run for a cancelled operation") })
	})

	h.key(tcell.KeyEsc, 0)
	select {
	case err := <-cancelled:
		if err == nil {
			t.Fatal("the context was not cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Esc did not cancel the operation")
	}
	h.waitIdle()
	if got := h.status(); !strings.Contains(got, "cancelled") {
		t.Fatalf("status = %q, want a cancellation notice", got)
	}
}

func TestProgressIsReported(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})

	proceed := make(chan struct{})
	h.do(func() {
		runAsync(h.c, "scanning",
			func(ctx context.Context, report func(string)) (int, error) {
				report("1234 keys scanned")
				<-proceed
				return 0, nil
			},
			func(int, error) {})
	})
	h.waitFor("the progress detail", func() bool {
		return strings.Contains(h.v.Status.GetText(true), "1234 keys scanned")
	})
	close(proceed)
	h.waitIdle()
}

func TestThrottledReportsAtMostOncePerInterval(t *testing.T) {
	n := 0
	report := throttled(func(string) { n++ }, func(done int) string { return "" })
	for i := 0; i < 1000; i++ {
		report(i)
	}
	if n > 2 {
		t.Fatalf("throttled reported %d times for a tight loop", n)
	}
}

func TestValueViewDecodesJSON(t *testing.T) {
	h := newHarness(t, map[string]string{"doc": `{"b":2,"a":1}`})
	h.do(func() { h.c.focus("doc") })

	d := h.details()
	if !strings.Contains(d, "json") {
		t.Fatalf("the encoding is not shown: %q", d)
	}
	if !strings.Contains(d, "\"a\": 1") {
		t.Fatalf("the document was not pretty printed: %q", d)
	}
}

func TestValueViewDecodesBase64OfGzip(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(gzipBytes(t, `{"hello":"world"}`)))
	h := newHarness(t, map[string]string{"blob": payload})
	h.do(func() { h.c.focus("blob") })

	d := h.details()
	if !strings.Contains(d, "base64 -> gzip -> json") {
		t.Fatalf("the decoding chain is not shown: %q", d)
	}
	if !strings.Contains(d, "hello") {
		t.Fatalf("the decoded document is not shown: %q", d)
	}
}

func TestValueViewCyclesRawAndHex(t *testing.T) {
	h := newHarness(t, map[string]string{"doc": `{"a":1}`})
	h.do(func() { h.c.focus("doc") })

	if got := h.c.valueMode.String(); got != "decoded" {
		t.Fatalf("initial mode = %q", got)
	}
	h.press(tcell.KeyCtrlV)
	if got := h.c.valueMode.String(); got != "raw" {
		t.Fatalf("mode after one switch = %q", got)
	}
	if d := h.details(); !strings.Contains(d, `{"a":1}`) || strings.Contains(d, "\"a\": 1") {
		t.Fatalf("raw view = %q", d)
	}

	h.press(tcell.KeyCtrlV)
	if got := h.c.valueMode.String(); got != "hex" {
		t.Fatalf("mode after two switches = %q", got)
	}
	if d := h.details(); !strings.Contains(d, "00000000") || !strings.Contains(d, "7b 22 61") {
		t.Fatalf("hex view = %q", d)
	}

	h.press(tcell.KeyCtrlV)
	if got := h.c.valueMode.String(); got != "decoded" {
		t.Fatalf("mode after three switches = %q", got)
	}
}

func TestBinaryValueFallsBackToHex(t *testing.T) {
	h := newHarness(t, map[string]string{"bin": "\x00\x01\x02\xff"})
	h.do(func() { h.c.focus("bin") })
	if d := h.details(); !strings.Contains(d, "00000000  00 01 02 ff") {
		t.Fatalf("a binary value should be shown as hex, got %q", d)
	}
}

func TestAnalyzeFillsFolderStatistics(t *testing.T) {
	h := newHarness(t, map[string]string{
		"app/a":     "1",
		"app/b/c":   "2",
		"unrelated": "3",
	})
	h.mr.HSet("app/h", "f", "v")
	h.press(tcell.KeyCtrlR)

	h.do(func() { h.c.focus("app/") })
	if d := h.details(); !strings.Contains(d, "Ctrl+A") {
		t.Fatalf("a folder should offer the analysis, got %q", d)
	}

	h.press(tcell.KeyCtrlA)
	h.waitFor("the report", func() bool { return h.frontPageNow() == "modal-report" })

	screen := h.screenText()
	for _, want := range []string{"Analysis", "Keys:", "Memory:", "Largest entries", "Largest keys"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the report does not contain %q:\n%s", want, screen)
		}
	}
	h.key(tcell.KeyEsc, 0)
	h.waitFor("the report to close", func() bool { return h.frontPageNow() == "main" })

	if d := h.details(); !strings.Contains(d, "Keys:") || !strings.Contains(d, "Memory:") {
		t.Fatalf("the folder details do not show the analysis: %q", d)
	}
	if !strings.Contains(h.details(), "hash 1") {
		t.Fatalf("the type breakdown is missing: %q", h.details())
	}
}

func TestAnalyzeCountsOnlyTheSelectedSubtree(t *testing.T) {
	h := newHarness(t, map[string]string{
		"app/a":    "1",
		"app/b":    "2",
		"other/c":  "3",
		"toplevel": "4",
	})
	h.do(func() { h.c.focus("app/") })
	h.press(tcell.KeyCtrlA)
	h.waitFor("the report", func() bool { return h.frontPageNow() == "main" || h.frontPageNow() == "modal-report" })

	var st *model.DirStats
	h.do(func() { st = h.c.cur().stats["app/"] })
	if st == nil {
		t.Fatal("no statistics were cached")
	}
	if st.Keys != 2 {
		t.Fatalf("Keys = %d, want the two keys of /app", st.Keys)
	}
}

func TestTogglePanes(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	if h.c.Dual() {
		t.Fatal("the second pane must start hidden")
	}

	h.press(tcell.KeyF9)
	h.waitFor("the second pane", func() bool { return h.c.Dual() })
	if h.v.Lists[1].GetItemCount() == 0 {
		t.Fatal("the second pane was not filled")
	}

	h.press(tcell.KeyF9)
	if h.c.Dual() {
		t.Fatal("the second pane was not hidden again")
	}
}

func TestSwitchPanes(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.press(tcell.KeyF9)
	h.waitFor("the second pane", func() bool { return h.c.Dual() })

	h.press(tcell.KeyTab)
	if h.c.active != 1 || h.v.List != h.v.Lists[1] {
		t.Fatalf("active pane = %d", h.c.active)
	}
	h.press(tcell.KeyTab)
	if h.c.active != 0 {
		t.Fatalf("active pane = %d", h.c.active)
	}

	// Leaving dual mode always returns to the first pane.
	h.press(tcell.KeyTab)
	h.press(tcell.KeyF9)
	if h.c.active != 0 || h.v.List != h.v.Lists[0] {
		t.Fatalf("active pane after hiding the second one = %d", h.c.active)
	}
}

func TestTabIsIgnoredWithoutTheSecondPane(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.press(tcell.KeyTab)
	if h.c.active != 0 {
		t.Fatalf("active pane = %d", h.c.active)
	}
}

func TestCopyToTheOtherPane(t *testing.T) {
	h := newHarness(t, map[string]string{"src/a": "1", "src/deep/b": "2"})
	h.press(tcell.KeyF9)
	h.waitFor("the second pane", func() bool { return h.c.Dual() })

	h.act(func() {
		h.c.panes[1].prefix = "backup/"
		h.c.reloadPane(h.c.panes[1], nil)
	})

	n := h.node("src|dir")
	h.act(func() {
		h.c.runTransfer(h.c.panes[0], h.c.panes[1], n, "backup/src", false)
	})

	if v, _ := h.mr.Get("backup/src/a"); v != "1" {
		t.Fatalf("backup/src/a = %q", v)
	}
	if v, _ := h.mr.Get("backup/src/deep/b"); v != "2" {
		t.Fatalf("backup/src/deep/b = %q", v)
	}
	if !h.mr.Exists("src/a") {
		t.Fatal("a copy must keep the source")
	}
}

func TestMoveToTheOtherPane(t *testing.T) {
	h := newHarness(t, map[string]string{"src/a": "1"})
	h.press(tcell.KeyF9)
	h.waitFor("the second pane", func() bool { return h.c.Dual() })

	h.act(func() {
		h.c.panes[1].prefix = "archive/"
		h.c.reloadPane(h.c.panes[1], nil)
	})

	n := h.node("src|dir")
	h.act(func() {
		h.c.runTransfer(h.c.panes[0], h.c.panes[1], n, "archive/src", true)
	})

	if v, _ := h.mr.Get("archive/src/a"); v != "1" {
		t.Fatalf("archive/src/a = %q", v)
	}
	if h.mr.Exists("src/a") {
		t.Fatal("a move must remove the source")
	}
}

func TestTransferReportsFailures(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1", "dst/a": "already there"})
	h.press(tcell.KeyF9)
	h.waitFor("the second pane", func() bool { return h.c.Dual() })
	h.act(func() {
		h.c.panes[1].prefix = "dst/"
		h.c.reloadPane(h.c.panes[1], nil)
	})

	n := h.node("a|file")
	h.act(func() {
		h.c.runTransfer(h.c.panes[0], h.c.panes[1], n, "dst/a", false)
	})
	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q, want an error", got)
	}
	if v, _ := h.mr.Get("dst/a"); v != "already there" {
		t.Fatalf("the destination was overwritten: %q", v)
	}
}

func TestTransferWithoutTheSecondPaneIsRefused(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.press(tcell.KeyF5)
	if got := h.status(); !strings.Contains(got, "F9") {
		t.Fatalf("status = %q, want a hint about the second pane", got)
	}
}

func TestTransferDialogShowsTheTarget(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.press(tcell.KeyF9)
	h.waitFor("the second pane", func() bool { return h.c.Dual() })
	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.press(tcell.KeyF5)

	if got := h.frontPage(); got != "modal" {
		t.Fatalf("front page = %q, want the confirmation", got)
	}
	if s := h.screenText(); !strings.Contains(s, "Copy a") {
		t.Fatalf("the dialog does not describe the copy:\n%s", s)
	}
	h.do(func() { h.v.Pages.RemovePage("modal") })
}

func TestSwitchDatabase(t *testing.T) {
	h := newHarness(t, map[string]string{"in-db0": "1"})
	h.c.SetConnect(func(ctx context.Context, db int) (*model.Model, error) {
		return model.New(model.Options{Host: h.mr.Host(), Port: h.mr.Port(), DB: db})
	})

	h.mr.Select(1)
	h.mr.Set("in-db1", "1")
	h.mr.Select(0)

	h.act(func() { h.c.switchDatabase(h.c.cur(), 1) })

	if got := h.c.cur().model.DB(); got != 1 {
		t.Fatalf("the pane is connected to database %d", got)
	}
	items := h.items()
	if len(items) != 2 || items[1] != "   in-db1" {
		t.Fatalf("listing after the switch = %v", items)
	}
}

func TestSwitchDatabaseWithoutAFactory(t *testing.T) {
	h := newHarness(t, map[string]string{"a": "1"})
	h.press(tcell.KeyCtrlD)
	if got := h.status(); !strings.Contains(got, "not available") {
		t.Fatalf("status = %q", got)
	}
}

// fakeEditor writes a script that replaces the file it is given.
func fakeEditor(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "editor.sh")
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExternalEditorSavesTheResult(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "before"})
	h.c.SetEditor(fakeEditor(t, `printf 'after' > "$1"`))

	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.act(func() { h.c.editExternally() })

	if v, _ := h.mr.Get("k"); v != "after" {
		t.Fatalf("k = %q, want the edited value", v)
	}
}

func TestExternalEditorPassesTheCurrentValue(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "original"})
	h.c.SetEditor(fakeEditor(t, `cat "$1" > "$1.copy"; printf 'x' > "$1"`))

	var seen string
	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.act(func() {
		h.c.loadForEdit(h.c.cur().nodes["k|file"], func(full *model.Node) {
			edited, _, err := h.c.runEditor(full)
			if err != nil {
				t.Errorf("runEditor: %v", err)
				return
			}
			seen = edited
		})
	})
	if seen != "x" {
		t.Fatalf("the editor result = %q", seen)
	}
}

func TestExternalEditorLeavesUnchangedValuesAlone(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "same"})
	h.c.SetEditor(fakeEditor(t, `true`))

	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.act(func() { h.c.editExternally() })

	if got := h.status(); !strings.Contains(got, "unchanged") {
		t.Fatalf("status = %q", got)
	}
	if v, _ := h.mr.Get("k"); v != "same" {
		t.Fatalf("k = %q", v)
	}
}

func TestExternalEditorReportsFailure(t *testing.T) {
	h := newHarness(t, map[string]string{"k": "v"})
	h.c.SetEditor(fakeEditor(t, `exit 3`))

	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.act(func() { h.c.editExternally() })

	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q, want an error", got)
	}
}

func TestExternalEditorRefusesBrokenJSON(t *testing.T) {
	h := newHarness(t, map[string]string{"k": `{"a":1}`})
	h.c.SetEditor(fakeEditor(t, `printf '{"a":' > "$1"`))

	h.do(func() { h.v.List.SetCurrentItem(1) })
	h.act(func() { h.c.editExternally() })

	if got := h.frontPage(); got != "modal-error" {
		t.Fatalf("front page = %q, want the validation error", got)
	}
	if v, _ := h.mr.Get("k"); v != `{"a":1}` {
		t.Fatalf("the broken document was stored: %q", v)
	}
}

func TestEditorCommandResolution(t *testing.T) {
	h := newHarness(t, nil)

	h.c.SetEditor("")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := h.c.editorCommand(); len(got) != 1 || got[0] != "vi" {
		t.Fatalf("default editor = %v", got)
	}
	t.Setenv("EDITOR", "nano -w")
	if got := h.c.editorCommand(); strings.Join(got, " ") != "nano -w" {
		t.Fatalf("$EDITOR was ignored: %v", got)
	}
	t.Setenv("VISUAL", "gvim")
	if got := h.c.editorCommand(); got[0] != "gvim" {
		t.Fatalf("$VISUAL must win over $EDITOR: %v", got)
	}
	h.c.SetEditor("code -w")
	if got := h.c.editorCommand(); strings.Join(got, " ") != "code -w" {
		t.Fatalf("the explicit editor must win: %v", got)
	}
}
