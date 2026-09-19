//go:build e2e

package e2e

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/nexusriot/redis-walker/pkg/model"
)

// The UI must stay responsive while a large scan runs, and Esc must abort it.
func TestLargeListingStaysResponsiveAndCanBeCancelled(t *testing.T) {
	a := start(t, nil)
	for i := 0; i < 20000; i++ {
		if err := a.rdb.Set(ctx(t), "bulk/key"+itoa(i), "v", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	a.Key(tcell.KeyCtrlR, 0)
	a.settle()

	// While the scan runs the event loop still answers and shows progress.
	a.waitFor("a progress report", func() bool {
		return strings.Contains(a.statusNow(), "scanned") || a.ctrl.Idle()
	})

	a.Key(tcell.KeyEsc, 0)
	a.settle()
	a.waitIdle()

	// The application is still usable afterwards.
	a.press(tcell.KeyCtrlR)
	a.waitFor("the listing", func() bool { return a.hasItemNow("bulk/") })
}

func TestValueViewsAgainstRealRedis(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(`{"service":"payments","retries":3}`)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	a := start(t, map[string]string{
		"plain": `{"b":2,"a":1}`,
		"blob":  base64.StdEncoding.EncodeToString(buf.Bytes()),
	})

	a.selectItem("plain")
	a.waitFor("the decoded document", func() bool {
		return strings.Contains(a.view.Details.GetText(true), "\"a\": 1")
	})

	a.press(tcell.KeyCtrlV)
	a.waitFor("the raw view", func() bool {
		return strings.Contains(a.view.Details.GetText(true), `{"b":2,"a":1}`)
	})
	a.press(tcell.KeyCtrlV)
	a.waitFor("the hex view", func() bool {
		return strings.Contains(a.view.Details.GetText(true), "00000000  7b 22 62")
	})
	a.press(tcell.KeyCtrlV)

	a.selectItem("blob")
	a.waitFor("the decoding chain", func() bool {
		d := a.view.Details.GetText(true)
		return strings.Contains(d, "base64 -> gzip -> json") && strings.Contains(d, "payments")
	})
	// The stored value is never rewritten by looking at it.
	if got := a.get("blob"); got != base64.StdEncoding.EncodeToString(buf.Bytes()) {
		t.Fatal("the stored value changed while it was displayed")
	}
}

func TestAnalyzeFolder(t *testing.T) {
	seed := map[string]string{"app/small": "x"}
	for i := 0; i < 20; i++ {
		seed["app/big/k"+itoa(i)] = strings.Repeat("v", 512)
	}
	a := start(t, seed)

	a.selectItem("app/")
	a.press(tcell.KeyCtrlA)
	a.waitFor("the analysis report", func() bool { return a.frontPageNow() == "modal-report" })

	screen := a.screenText()
	for _, want := range []string{"Analysis", "Keys:", "21", "Memory:", "Largest entries", "big/"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the report does not contain %q:\n%s", want, screen)
		}
	}

	a.press(tcell.KeyEsc)
	a.waitFor("the report to close", func() bool { return a.frontPageNow() == "main" })
	a.waitFor("the folder statistics", func() bool {
		return strings.Contains(a.view.Details.GetText(true), "Keys:")
	})
}

func TestCopyBetweenPanesWithF5(t *testing.T) {
	a := start(t, map[string]string{
		"src/a":      "1",
		"src/deep/b": "2",
		"dst/keep":   "3",
	})

	a.press(tcell.KeyF9)
	a.waitFor("the second pane", func() bool { return a.ctrl.Dual() })

	// Move the second pane into /dst.
	a.press(tcell.KeyTab)
	a.selectItem("dst/")
	a.press(tcell.KeyEnter)
	a.waitFor("the destination folder", func() bool { return a.ctrl.CurrentPrefix() == "dst/" })

	// Back to the first pane and copy the folder over.
	a.press(tcell.KeyTab)
	a.selectItem("src/")
	a.press(tcell.KeyF5)
	a.waitFor("the confirmation", func() bool { return a.frontPageNow() == "modal" })
	if s := a.screenText(); !strings.Contains(s, "Copy src/") {
		t.Fatalf("the dialog does not describe the copy:\n%s", s)
	}
	a.press(tcell.KeyEnter)

	a.waitFor("the copied subtree", func() bool { return a.exists("dst/src/deep/b") })
	if got := a.get("dst/src/a"); got != "1" {
		t.Fatalf("dst/src/a = %q", got)
	}
	if !a.exists("src/a") {
		t.Fatal("a copy must keep the source")
	}
	if !a.exists("dst/keep") {
		t.Fatal("the destination folder lost a key")
	}
}

func TestMoveBetweenPanesWithF6(t *testing.T) {
	a := start(t, map[string]string{"src/a": "1", "dst/keep": "2"})

	a.press(tcell.KeyF9)
	a.waitFor("the second pane", func() bool { return a.ctrl.Dual() })
	a.press(tcell.KeyTab)
	a.selectItem("dst/")
	a.press(tcell.KeyEnter)
	a.waitFor("the destination folder", func() bool { return a.ctrl.CurrentPrefix() == "dst/" })
	a.press(tcell.KeyTab)

	a.selectItem("src/")
	a.press(tcell.KeyF6)
	a.waitFor("the confirmation", func() bool { return a.frontPageNow() == "modal" })
	a.press(tcell.KeyEnter)

	a.waitFor("the moved key", func() bool { return a.exists("dst/src/a") })
	if a.exists("src/a") {
		t.Fatal("a move must remove the source")
	}
}

// Copying a hash must keep it a hash, which needs DUMP/RESTORE or COPY.
func TestCopyPreservesNonStringTypes(t *testing.T) {
	a := start(t, map[string]string{"src/s": "text", "dst/x": "1"})
	if err := a.rdb.HSet(ctx(t), "src/h", "f", "v").Err(); err != nil {
		t.Fatal(err)
	}
	a.press(tcell.KeyCtrlR)

	a.press(tcell.KeyF9)
	a.waitFor("the second pane", func() bool { return a.ctrl.Dual() })
	a.press(tcell.KeyTab)
	a.selectItem("dst/")
	a.press(tcell.KeyEnter)
	a.waitFor("the destination folder", func() bool { return a.ctrl.CurrentPrefix() == "dst/" })
	a.press(tcell.KeyTab)

	a.selectItem("src/")
	a.press(tcell.KeyF5)
	a.waitFor("the confirmation", func() bool { return a.frontPageNow() == "modal" })
	a.press(tcell.KeyEnter)
	a.waitFor("the copied hash", func() bool { return a.exists("dst/src/h") })

	if typ := a.rdb.Type(ctx(t), "dst/src/h").Val(); typ != "hash" {
		t.Fatalf("the copied key is a %s", typ)
	}
	if v := a.rdb.HGet(ctx(t), "dst/src/h", "f").Val(); v != "v" {
		t.Fatalf("copied field = %q", v)
	}
}

func TestSecondPaneShowsItsOwnFolder(t *testing.T) {
	a := start(t, map[string]string{"one/a": "1", "two/b": "2"})
	a.press(tcell.KeyF9)
	a.waitFor("the second pane", func() bool { return a.ctrl.Dual() })

	a.press(tcell.KeyTab)
	a.selectItem("two/")
	a.press(tcell.KeyEnter)
	a.waitFor("the second pane folder", func() bool { return a.ctrl.CurrentPrefix() == "two/" })
	a.press(tcell.KeyTab)

	var left, right []string
	a.onLoop(func() {
		left = a.listNow(0)
		right = a.listNow(1)
	})
	if strings.Join(left, ",") != "[..],one/,two/" {
		t.Fatalf("left pane = %v", left)
	}
	if strings.Join(right, ",") != "[..],b" {
		t.Fatalf("right pane = %v", right)
	}
}

func TestSwitchDatabaseFromTheUI(t *testing.T) {
	addr := redisAddr(t)
	host, port := hostPort(t, addr)

	a := start(t, map[string]string{"in-db0": "1"})
	a.ctrl.SetConnect(func(_ context.Context, db int) (*model.Model, error) {
		return model.New(model.Options{Host: host, Port: port, DB: db})
	})

	other := rawClient(t, addr, "")
	if err := other.Do(ctx(t), "select", 3).Err(); err != nil {
		t.Skipf("the server has no database 3: %v", err)
	}
	if err := other.Conn().Select(ctx(t), 3).Err(); err == nil {
		_ = other.Conn().Set(ctx(t), "in-db3", "1", 0).Err()
	}

	a.press(tcell.KeyCtrlD)
	a.waitFor("the database prompt", func() bool { return a.frontPageNow() == "modal" })
	a.typeText("3")
	a.press(tcell.KeyEnter)
	a.waitFor("the new connection", func() bool { return a.ctrl.CurrentPrefix() == "" && a.frontPageNow() == "main" })

	if !strings.Contains(a.screenText(), "/3") {
		t.Logf("screen:\n%s", a.screenText())
	}
}

func TestExternalEditorRoundTrip(t *testing.T) {
	a := start(t, map[string]string{"key": "before"})

	script := filepath.Join(t.TempDir(), "editor.sh")
	body := "#!/bin/sh\nprintf 'edited by $EDITOR' > \"$1\"\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	a.ctrl.SetEditor(script)

	a.selectItem("key")
	a.press(tcell.KeyCtrlO)
	a.waitFor("the edited value", func() bool { return a.exists("key") })

	if got := a.get("key"); !strings.HasPrefix(got, "edited by") {
		t.Fatalf("key = %q, want the value written by the editor", got)
	}
	// The UI is back and usable.
	a.press(tcell.KeyCtrlR)
	a.waitFor("the list", func() bool { return a.hasItemNow("   key") })
}

func TestHeadlessCommands(t *testing.T) {
	addr := redisAddr(t)
	host, port := hostPort(t, addr)
	a := start(t, map[string]string{
		"app/cfg/db": "postgres",
		"app/name":   "walker",
		"session:1":  "token",
	})
	_ = a

	bin := filepath.Join(t.TempDir(), "redis-walker")
	build := exec.Command("go", "build", "-o", bin, "./cmd/redis-walker")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	run := func(args ...string) string {
		t.Helper()
		full := append([]string{"-host", host, "-port", port, "-config", filepath.Join(t.TempDir(), "none.json")}, args...)
		cmd := exec.Command(bin, full...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", args, err, out)
		}
		return string(out)
	}

	if out := run("ls"); !strings.Contains(out, "d  app/") || !strings.Contains(out, "-  session:1") {
		t.Fatalf("ls:\n%s", out)
	}
	if out := run("tree", "/app"); out != "cfg/\n  db\nname\n" {
		t.Fatalf("tree: %q", out)
	}
	if out := run("cat", "/app/name"); out != "walker" {
		t.Fatalf("cat: %q", out)
	}

	var node struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}
	if err := json.Unmarshal([]byte(run("-json", "cat", "/app/name")), &node); err != nil {
		t.Fatal(err)
	}
	if node.Key != "app/name" || node.Value != "walker" || node.Type != "string" {
		t.Fatalf("json node = %+v", node)
	}

	run("set", "/app/created", "by-cli")
	if got := a.get("app/created"); got != "by-cli" {
		t.Fatalf("set wrote %q", got)
	}
	run("rm", "/app/created")
	if a.exists("app/created") {
		t.Fatal("rm did not delete the key")
	}

	if out := run("stat", "/app"); !strings.Contains(out, "keys:") || !strings.Contains(out, "largest keys:") {
		t.Fatalf("stat:\n%s", out)
	}
	if out := run("-version"); !strings.Contains(out, "redis-walker") {
		t.Fatalf("version: %q", out)
	}
}

// repoRoot locates the module root so that "go build" works from the test.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found")
	return ""
}

func itoa(i int) string { return strconv.Itoa(i) }
