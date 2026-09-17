//go:build e2e

// Package e2e drives the complete redis-walker application - the real
// controller, the real tview widgets and a real Redis server - through a tcell
// simulation screen. It only uses the public API and injected key presses, so
// it exercises the same code path as an interactive terminal session.
package e2e

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nexusriot/redis-walker/pkg/controller"
	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
)

const (
	envAddr         = "REDIS_ADDR"
	envAuthAddr     = "REDIS_AUTH_ADDR"
	envAuthPassword = "REDIS_AUTH_PASSWORD"
)

// hostPort splits "host:port" into its parts.
func hostPort(t *testing.T, addr string) (string, string) {
	t.Helper()
	host, port, ok := strings.Cut(addr, ":")
	if !ok {
		t.Fatalf("%q is not a host:port pair", addr)
	}
	return host, port
}

func redisAddr(t *testing.T) string {
	t.Helper()
	addr := os.Getenv(envAddr)
	if addr == "" {
		t.Skipf("%s is not set; run the suite with test/e2e/docker-compose.yml", envAddr)
	}
	return addr
}

// rawClient is used to assert on the server state independently of the model.
func rawClient(t *testing.T, addr, password string) *redis.Client {
	t.Helper()
	c := redis.NewClient(&redis.Options{Addr: addr, Password: password})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// app is a running redis-walker instance on a simulation screen.
type app struct {
	t      *testing.T
	rdb    *redis.Client
	view   *view.View
	ctrl   *controller.Controller
	screen tcell.SimulationScreen
	done   chan error
}

// start flushes the database, seeds it and launches the application.
func start(t *testing.T, seed map[string]string) *app {
	t.Helper()

	addr := redisAddr(t)
	host, port := hostPort(t, addr)
	rdb := rawClient(t, addr, "")
	if err := rdb.FlushDB(ctx(t)).Err(); err != nil {
		t.Fatalf("flushdb: %v", err)
	}
	for k, v := range seed {
		if err := rdb.Set(ctx(t), k, v, 0).Err(); err != nil {
			t.Fatalf("seed %q: %v", k, err)
		}
	}

	m, err := model.New(model.Options{Host: host, Port: port})
	if err != nil {
		t.Fatalf("model.New: %v", err)
	}

	v := view.NewView()
	v.SetHeader("redis-walker e2e")
	screen := tcell.NewSimulationScreen("UTF-8")
	v.App.SetScreen(screen)
	screen.SetSize(140, 40)

	a := &app{
		t:      t,
		rdb:    rdb,
		view:   v,
		ctrl:   controller.New(m, v, true),
		screen: screen,
		done:   make(chan error, 1),
	}
	go func() { a.done <- a.ctrl.Run() }()

	t.Cleanup(func() {
		v.App.Stop()
		select {
		case <-a.done:
		case <-time.After(10 * time.Second):
			t.Error("the application did not stop")
		}
		_ = m.Close()
	})

	a.sync()
	return a
}

// onLoop runs f on the tview event loop and waits for it to finish. It must
// only be called from the test goroutine: calling it from inside another
// queued closure would deadlock the loop.
func (a *app) onLoop(f func()) {
	a.t.Helper()
	done := make(chan struct{})
	a.view.App.QueueUpdateDraw(func() {
		defer close(done)
		f()
	})
	select {
	case <-done:
	case err := <-a.done:
		a.t.Fatalf("the application stopped early: %v", err)
	case <-time.After(10 * time.Second):
		a.t.Fatal("timed out waiting for the UI")
	}
}

// sync waits until every queued UI update has been drawn.
func (a *app) sync() { a.onLoop(func() {}) }

// press injects a key and waits for the UI to settle.
func (a *app) press(k tcell.Key) {
	a.screen.InjectKey(k, 0, tcell.ModNone)
	a.settle()
}

// typeText injects a string one rune at a time.
func (a *app) typeText(s string) {
	for _, r := range s {
		a.screen.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	a.settle()
}

// settle gives the event loop time to consume injected events.
func (a *app) settle() {
	a.sync()
	a.sync()
}

// waitFor polls a condition against the UI. cond runs on the event loop, so it
// may only use the "...Now" readers below.
func (a *app) waitFor(what string, cond func() bool) {
	a.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		a.onLoop(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.t.Fatalf("timed out waiting for %s\nscreen:\n%s", what, a.screenText())
}

// screenTextNow renders the simulation screen; must run on the event loop.
func (a *app) screenTextNow() string {
	var sb strings.Builder
	cells, w, h := a.screen.GetContents()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			runes := cells[y*w+x].Runes
			if len(runes) == 0 {
				sb.WriteRune(' ')
				continue
			}
			sb.WriteRune(runes[0])
		}
		sb.WriteRune('\n')
	}
	return sb.String()
}

func (a *app) screenText() string {
	var out string
	a.onLoop(func() { out = a.screenTextNow() })
	return out
}

// itemsNow returns the labels of the list; must run on the event loop.
func (a *app) itemsNow() []string {
	out := make([]string, 0, a.view.List.GetItemCount())
	for i := 0; i < a.view.List.GetItemCount(); i++ {
		main, _ := a.view.List.GetItemText(i)
		out = append(out, main)
	}
	return out
}

func (a *app) items() []string {
	var out []string
	a.onLoop(func() { out = a.itemsNow() })
	return out
}

// hasItem reports whether the list contains a label; must run on the loop.
func (a *app) hasItemNow(label string) bool {
	for _, it := range a.itemsNow() {
		if it == label {
			return true
		}
	}
	return false
}

// frontPageNow returns the name of the topmost page; must run on the loop.
func (a *app) frontPageNow() string {
	name, _ := a.view.Pages.GetFrontPage()
	return name
}

// colourTag matches a real tview colour/style tag, as tview itself defines it.
// An escaped tag ("cache[1[]") deliberately does not match.
var colourTag = regexp.MustCompile(`\[(?:[a-zA-Z]+|#[0-9a-zA-Z]{6}|-)?(?::(?:[a-zA-Z]+|#[0-9a-zA-Z]{6}|-)?)?(?::(?:[lbidrus]+|-)?)?\]`)

// escapedTag matches tview's escape form "[something[]".
var escapedTag = regexp.MustCompile(`\[([^\[\]]*)\[\]`)

// entryName reduces a list label to the plain name of the entry:
//
//	"   ahash [blue](hash)[-]" -> "ahash"
//	"📁 cache[1[]/"            -> "cache[1]/"
func entryName(label string) string {
	// Undo the escaping first: "cache[1[]" contains a "[]" that would
	// otherwise be eaten as an empty colour tag.
	name := escapedTag.ReplaceAllString(label, "[$1]")
	name = colourTag.ReplaceAllString(name, "")
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "📁"))
	if base, _, ok := strings.Cut(name, " ("); ok {
		name = strings.TrimSpace(base)
	}
	return name
}

// selectItem moves the cursor onto the list entry with the given label.
func (a *app) selectItem(label string) {
	a.t.Helper()
	a.waitFor("list entry "+label, func() bool {
		for i := 0; i < a.view.List.GetItemCount(); i++ {
			main, _ := a.view.List.GetItemText(i)
			if entryName(main) == label {
				a.view.List.SetCurrentItem(i)
				return true
			}
		}
		return false
	})
	a.settle()
}

func (a *app) currentItem() string {
	var out string
	a.onLoop(func() {
		main, _ := a.view.List.GetItemText(a.view.List.GetCurrentItem())
		out = main
	})
	return out
}

func (a *app) get(key string) string {
	a.t.Helper()
	v, err := a.rdb.Get(ctx(a.t), key).Result()
	if err != nil {
		a.t.Fatalf("GET %q: %v", key, err)
	}
	return v
}

func (a *app) exists(key string) bool {
	a.t.Helper()
	n, err := a.rdb.Exists(ctx(a.t), key).Result()
	if err != nil {
		a.t.Fatalf("EXISTS %q: %v", key, err)
	}
	return n > 0
}
