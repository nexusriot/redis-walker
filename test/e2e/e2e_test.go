//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/redis/go-redis/v9"
	"github.com/rivo/tview"

	"github.com/nexusriot/redis-walker/pkg/model"
)

func TestBrowseTree(t *testing.T) {
	a := start(t, map[string]string{
		"app/cfg/db":   "postgres",
		"app/cfg/web":  "nginx",
		"app/version":  "1.2.3",
		"session:42":   "token",
		"legacy/entry": "old",
	})

	want := []string{"[..]", "📁 app/", "📁 legacy/", "   session:42"}
	if got := a.items(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("root listing = %v, want %v", got, want)
	}

	// Enter the "app" folder.
	a.selectItem("app/")
	a.press(tcell.KeyEnter)
	a.waitFor("the app folder", func() bool { return a.ctrl.CurrentPrefix() == "app/" })
	if got := a.items(); strings.Join(got, "|") != "[..]|📁 cfg/|   version" {
		t.Fatalf("app listing = %v", got)
	}

	// Descend once more and come back with Backspace.
	a.selectItem("cfg/")
	a.press(tcell.KeyEnter)
	a.waitFor("the cfg folder", func() bool { return a.ctrl.CurrentPrefix() == "app/cfg/" })
	a.press(tcell.KeyBackspace2)
	a.waitFor("the app folder again", func() bool { return a.ctrl.CurrentPrefix() == "app/" })
	if got := a.currentItem(); got != "📁 cfg/" {
		t.Fatalf("the cursor did not return to the folder we came from: %q", got)
	}
}

func TestDetailsPaneShowsValue(t *testing.T) {
	a := start(t, map[string]string{"greeting": "hello world"})
	a.selectItem("greeting")
	a.waitFor("the value in the details pane", func() bool {
		return strings.Contains(a.view.Details.GetText(true), "hello world")
	})
	screen := a.screenText()
	for _, want := range []string{"/greeting", "string", "11 bytes", "hello world"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the details pane does not show %q:\n%s", want, screen)
		}
	}
}

func TestCreateKeyThroughTheDialog(t *testing.T) {
	a := start(t, map[string]string{"placeholder": "1"})

	a.press(tcell.KeyCtrlN)
	a.waitFor("the create dialog", func() bool { return a.frontPageNow() == "modal" })
	a.typeText("created")
	a.press(tcell.KeyTab) // -> Value
	a.typeText("from-the-ui")
	a.press(tcell.KeyTab) // -> Is a Directory
	a.press(tcell.KeyTab) // -> Save
	a.press(tcell.KeyEnter)

	a.waitFor("the dialog to close", func() bool { return a.frontPageNow() == "main" })
	if got := a.get("created"); got != "from-the-ui" {
		t.Fatalf("created = %q", got)
	}
	if a.exists("/created") {
		t.Fatal("the key was written with a spurious leading slash")
	}
	if got := a.currentItem(); got != "   created" {
		t.Fatalf("the cursor is on %q, want the new key", got)
	}
}

func TestCreateFolderThroughTheDialog(t *testing.T) {
	a := start(t, map[string]string{"placeholder": "1"})

	a.press(tcell.KeyCtrlN)
	a.waitFor("the create dialog", func() bool { return a.frontPageNow() == "modal" })
	a.typeText("folder")
	a.press(tcell.KeyTab)
	a.press(tcell.KeyTab) // -> Is a Directory
	a.typeText(" ")       // toggle the checkbox
	a.press(tcell.KeyTab) // -> Save
	a.press(tcell.KeyEnter)

	a.waitFor("the new folder", func() bool { return a.hasItemNow("📁 folder/") })
	if !a.exists("folder/.dir") {
		t.Fatal("the folder marker was not created")
	}
}

func TestEditValueThroughTheEditor(t *testing.T) {
	a := start(t, map[string]string{"key": "original"})

	a.selectItem("key")
	a.press(tcell.KeyCtrlE)
	a.waitFor("the editor", func() bool {
		_, ok := a.view.App.GetFocus().(*tview.TextArea)
		return ok
	})
	a.typeText("EDITED-")
	a.press(tcell.KeyCtrlS)

	a.waitFor("the edited value", func() bool { return a.get("key") == "EDITED-original" })
	if got := a.currentItem(); got != "   key" {
		t.Fatalf("the cursor is on %q after saving", got)
	}
	a.waitFor("the details pane to refresh", func() bool {
		return strings.Contains(a.view.Details.GetText(true), "EDITED-original")
	})
}

func TestEditorEscapeDiscardsChanges(t *testing.T) {
	a := start(t, map[string]string{"key": "original"})

	a.selectItem("key")
	a.press(tcell.KeyCtrlE)
	a.waitFor("the editor", func() bool {
		_, ok := a.view.App.GetFocus().(*tview.TextArea)
		return ok
	})
	a.typeText("DISCARD")
	a.press(tcell.KeyEsc)

	a.waitFor("the list to come back", func() bool { return a.view.App.GetFocus() == a.view.List })
	if got := a.get("key"); got != "original" {
		t.Fatalf("Esc saved the value anyway: %q", got)
	}
}

// Editing a large value must round-trip the whole string, not the preview that
// the listing keeps in memory.
func TestEditLargeValueKeepsEveryByte(t *testing.T) {
	big := strings.Repeat("0123456789", 5000) // 50 kB, far beyond the preview
	a := start(t, map[string]string{"big": big})

	a.selectItem("big")
	a.press(tcell.KeyCtrlE)
	a.waitFor("the editor", func() bool {
		ta, ok := a.view.App.GetFocus().(*tview.TextArea)
		return ok && len(ta.GetText()) == len(big)
	})
	a.press(tcell.KeyCtrlS)

	a.waitFor("the value to be written back unchanged", func() bool { return a.get("big") == big })
}

func TestEditRefusesNonStringValues(t *testing.T) {
	a := start(t, nil)
	if err := a.rdb.HSet(ctx(t), "ahash", "field", "value").Err(); err != nil {
		t.Fatal(err)
	}
	a.press(tcell.KeyCtrlR)
	a.selectItem("ahash")
	a.press(tcell.KeyCtrlE)

	a.waitFor("the error modal", func() bool { return a.frontPageNow() == "modal-error" })
	if typ := a.rdb.Type(ctx(t), "ahash").Val(); typ != "hash" {
		t.Fatalf("the hash was replaced by a %s", typ)
	}
}

func TestDeleteKeyThroughTheDialog(t *testing.T) {
	a := start(t, map[string]string{"doomed": "1", "kept": "1"})

	a.selectItem("doomed")
	a.press(tcell.KeyDelete)
	a.waitFor("the confirmation dialog", func() bool { return a.frontPageNow() == "modal" })
	if !strings.Contains(a.screenText(), "Delete doomed ?") {
		t.Fatalf("the dialog does not name the key:\n%s", a.screenText())
	}
	a.press(tcell.KeyEnter) // the "ok" button has focus

	a.waitFor("the key to disappear", func() bool { return !a.exists("doomed") })
	if !a.exists("kept") {
		t.Fatal("an unrelated key was deleted")
	}
}

func TestDeleteFolderIsRecursive(t *testing.T) {
	a := start(t, map[string]string{
		"tree/a":    "1",
		"tree/b/c":  "2",
		"treehouse": "keep",
	})

	a.selectItem("tree/")
	a.press(tcell.KeyDelete)
	a.waitFor("the confirmation dialog", func() bool { return a.frontPageNow() == "modal" })
	if !strings.Contains(a.screenText(), "recursive") {
		t.Fatal("the dialog does not warn about the recursive delete")
	}
	a.press(tcell.KeyEnter)

	a.waitFor("the subtree to disappear", func() bool {
		return !a.exists("tree/a") && !a.exists("tree/b/c")
	})
	if !a.exists("treehouse") {
		t.Fatal("a sibling key that merely shares the prefix was deleted")
	}
}

func TestDeleteCancelKeepsTheKey(t *testing.T) {
	a := start(t, map[string]string{"safe": "1"})

	a.selectItem("safe")
	a.press(tcell.KeyDelete)
	a.waitFor("the confirmation dialog", func() bool { return a.frontPageNow() == "modal" })
	a.press(tcell.KeyRight) // move to "cancel"
	a.press(tcell.KeyEnter)

	a.waitFor("the dialog to close", func() bool { return a.frontPageNow() == "main" })
	if !a.exists("safe") {
		t.Fatal("cancelling the dialog still deleted the key")
	}
}

func TestRenameFolderThroughTheDialog(t *testing.T) {
	a := start(t, map[string]string{"old/a": "1", "old/b/c": "2"})
	if err := a.rdb.Expire(ctx(t), "old/a", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	a.selectItem("old/")
	a.press(tcell.KeyCtrlE)
	a.waitFor("the rename dialog", func() bool { return a.frontPageNow() == "modal" })
	// Clear the prefilled name and type the new one.
	for i := 0; i < len("old"); i++ {
		a.screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	}
	a.settle()
	a.typeText("fresh")
	a.press(tcell.KeyTab) // -> Save
	a.press(tcell.KeyEnter)

	a.waitFor("the renamed folder", func() bool { return a.exists("fresh/a") })
	if a.exists("old/a") || a.exists("old/b/c") {
		t.Fatal("the source keys survived the rename")
	}
	if got := a.get("fresh/b/c"); got != "2" {
		t.Fatalf("fresh/b/c = %q", got)
	}
	ttl, err := a.rdb.TTL(ctx(t), "fresh/a").Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("the TTL was lost by the rename (ttl=%v err=%v)", ttl, err)
	}
}

// Renaming must move hashes and lists untouched instead of replacing them with
// an empty string.
func TestRenameFolderPreservesNonStringValues(t *testing.T) {
	a := start(t, map[string]string{"src/s": "text"})
	if err := a.rdb.HSet(ctx(t), "src/h", "f", "v").Err(); err != nil {
		t.Fatal(err)
	}
	if err := a.rdb.RPush(ctx(t), "src/l", "one", "two").Err(); err != nil {
		t.Fatal(err)
	}
	a.press(tcell.KeyCtrlR)

	a.selectItem("src/")
	a.press(tcell.KeyCtrlE)
	a.waitFor("the rename dialog", func() bool { return a.frontPageNow() == "modal" })
	for i := 0; i < len("src"); i++ {
		a.screen.InjectKey(tcell.KeyBackspace2, 0, tcell.ModNone)
	}
	a.settle()
	a.typeText("dst")
	a.press(tcell.KeyTab)
	a.press(tcell.KeyEnter)

	a.waitFor("the renamed keys", func() bool { return a.exists("dst/h") })
	if typ := a.rdb.Type(ctx(t), "dst/h").Val(); typ != "hash" {
		t.Fatalf("dst/h is a %s", typ)
	}
	if v := a.rdb.HGet(ctx(t), "dst/h", "f").Val(); v != "v" {
		t.Fatalf("dst/h field = %q", v)
	}
	if v := a.rdb.LRange(ctx(t), "dst/l", 0, -1).Val(); strings.Join(v, ",") != "one,two" {
		t.Fatalf("dst/l = %v", v)
	}
}

func TestJumpToAKey(t *testing.T) {
	a := start(t, map[string]string{"deep/nested/leaf": "found", "deep/nested/other": "x"})

	a.press(tcell.KeyCtrlJ)
	a.waitFor("the jump dialog", func() bool { return a.frontPageNow() == "modal" })
	a.typeText("/deep/nested/leaf")
	a.press(tcell.KeyEnter)

	a.waitFor("the target folder", func() bool { return a.ctrl.CurrentPrefix() == "deep/nested/" })
	if got := a.currentItem(); got != "   leaf" {
		t.Fatalf("the cursor is on %q", got)
	}
}

func TestJumpToAMissingKeyShowsAnError(t *testing.T) {
	a := start(t, map[string]string{"a": "1"})

	a.press(tcell.KeyCtrlJ)
	a.waitFor("the jump dialog", func() bool { return a.frontPageNow() == "modal" })
	a.typeText("/does/not/exist")
	a.press(tcell.KeyEnter)

	a.waitFor("the error modal", func() bool { return a.frontPageNow() == "modal-error" })
	if !strings.Contains(a.screenText(), "Not found") {
		t.Fatalf("the error is not readable on screen:\n%s", a.screenText())
	}
}

func TestHelpModal(t *testing.T) {
	a := start(t, map[string]string{"a": "1"})
	a.press(tcell.KeyF1)
	a.waitFor("the help modal", func() bool { return a.frontPageNow() == "modal-help" })
	screen := a.screenText()
	for _, want := range []string{"Hotkeys", "Ctrl+N", "Ctrl+J", "Ctrl+R"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the help modal does not mention %q", want)
		}
	}
	a.press(tcell.KeyEsc)
	a.waitFor("the help modal to close", func() bool { return a.frontPageNow() == "main" })
}

// Keys whose names contain glob meta characters must be listed and deleted
// exactly, never as a pattern.
func TestGlobMetaCharactersInKeyNames(t *testing.T) {
	a := start(t, map[string]string{
		"cache[1]/a": "1",
		"cache[1]/b": "2",
		"cache1/c":   "3",
		"cacheX/d":   "4",
		"star*/e":    "5",
		"star1/f":    "6",
	})

	a.selectItem("cache[1]/")
	a.press(tcell.KeyEnter)
	a.waitFor("the folder", func() bool { return a.ctrl.CurrentPrefix() == "cache[1]/" })
	if got := a.items(); strings.Join(got, "|") != "[..]|   a|   b" {
		t.Fatalf("listing = %v", got)
	}
	a.press(tcell.KeyBackspace2)

	a.selectItem("star*/")
	a.press(tcell.KeyDelete)
	a.waitFor("the confirmation dialog", func() bool { return a.frontPageNow() == "modal" })
	a.press(tcell.KeyEnter)
	a.waitFor("the folder to disappear", func() bool { return !a.exists("star*/e") })

	for _, k := range []string{"star1/f", "cache1/c", "cacheX/d"} {
		if !a.exists(k) {
			t.Fatalf("%q was deleted by a pattern match", k)
		}
	}
}

func TestExcludePrefixesHideKeys(t *testing.T) {
	addr := redisAddr(t)
	host, port := hostPort(t, addr)
	rdb := rawClient(t, addr, "")
	if err := rdb.FlushDB(ctx(t)).Err(); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"pcp:one", "metrics/two", "keep"} {
		if err := rdb.Set(ctx(t), k, "v", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	m, err := model.New(model.Options{
		Host: host, Port: port,
		ExcludePrefixes: []string{"/pcp:", "/metrics/"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	l, err := m.Ls("")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Nodes) != 1 || l.Nodes[0].Key != "keep" {
		t.Fatalf("listing = %+v, want only 'keep'", l.Nodes)
	}
}

func TestAuthentication(t *testing.T) {
	addr := os.Getenv(envAuthAddr)
	password := os.Getenv(envAuthPassword)
	if addr == "" || password == "" {
		t.Skipf("%s/%s are not set", envAuthAddr, envAuthPassword)
	}
	host, port := hostPort(t, addr)

	if _, err := model.New(model.Options{Host: host, Port: port}); err == nil {
		t.Fatal("connecting without a password must fail")
	}
	if _, err := model.New(model.Options{Host: host, Port: port, Password: "wrong"}); err == nil {
		t.Fatal("connecting with a wrong password must fail")
	}

	m, err := model.New(model.Options{Host: host, Port: port, Password: password})
	if err != nil {
		t.Fatalf("connecting with the right password failed: %v", err)
	}
	defer m.Close()

	if err := m.Set("authenticated", "yes"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	n, err := m.Get("authenticated")
	if err != nil || n.Value != "yes" {
		t.Fatalf("Get = %+v, %v", n, err)
	}
	if err := m.Del("authenticated"); err != nil {
		t.Fatalf("Del: %v", err)
	}
}

func TestSelectedDatabaseIsIsolated(t *testing.T) {
	addr := redisAddr(t)
	host, port := hostPort(t, addr)

	db0, err := model.New(model.Options{Host: host, Port: port, DB: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer db0.Close()
	db1, err := model.New(model.Options{Host: host, Port: port, DB: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()

	raw1 := redis.NewClient(&redis.Options{Addr: addr, DB: 1})
	defer raw1.Close()
	if err := raw1.FlushDB(ctx(t)).Err(); err != nil {
		t.Fatal(err)
	}
	if err := db1.Set("only-in-db1", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := db0.Get("only-in-db1"); err == nil {
		t.Fatal("database 0 sees a key written to database 1")
	}
	if err := db1.Del("only-in-db1"); err != nil {
		t.Fatal(err)
	}
}
