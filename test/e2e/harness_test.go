//go:build e2e

// Package e2e drives the complete redis-walker application - the real
// controller, the real tview widgets and a real Redis server - through a tcell
// simulation screen. It only uses the public API and injected key presses, so
// it exercises the same code path as an interactive terminal session.
package e2e

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nexusriot/redis-walker/internal/uitest"
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
	*uitest.Driver
	t    *testing.T
	rdb  *redis.Client
	view *view.View
	ctrl *controller.Controller
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
	t.Cleanup(func() { _ = m.Close() })

	v := view.NewView()
	v.SetHeader("redis-walker e2e")

	a := &app{t: t, rdb: rdb, view: v, ctrl: controller.New(m, v, true)}
	a.Driver = uitest.New(t, v.App, 140, 40)
	a.Start(a.ctrl.Run)

	a.Sync()
	a.waitIdle()
	return a
}

// onLoop runs f on the event loop and waits for it.
func (a *app) onLoop(f func()) { a.OnLoop(f) }

// sync waits until every queued UI update has been drawn.
func (a *app) sync() { a.Sync() }

// settle gives the event loop time to consume injected events.
func (a *app) settle() { a.Settle() }

// waitFor polls a condition against the UI.
func (a *app) waitFor(what string, cond func() bool) { a.WaitFor(what, cond) }

// waitIdle blocks until no background operation is running.
func (a *app) waitIdle() {
	a.t.Helper()
	a.waitFor("the controller to become idle", func() bool { return a.ctrl.Idle() })
}

// press injects a key and waits for the UI and any work it started.
func (a *app) press(k tcell.Key) {
	a.Key(k, 0)
	a.settle()
	a.waitIdle()
	a.settle()
}

// typeText injects a string one rune at a time.
func (a *app) typeText(s string) { a.Type(s) }

// screenText renders the simulation screen as text.
func (a *app) screenText() string { return a.ScreenText() }

// statusNow returns the bottom status line; must run on the event loop.
func (a *app) statusNow() string { return a.view.Status.GetText(true) }

// frontPageNow returns the name of the topmost page; must run on the loop.
func (a *app) frontPageNow() string {
	name, _ := a.view.Pages.GetFrontPage()
	return name
}

// itemsNow returns the labels of the active list; must run on the loop.
func (a *app) itemsNow() []string { return uitest.ItemsNow(a.view.List) }

// listNow returns the plain entry names of a specific pane; must run on the loop.
func (a *app) listNow(i int) []string {
	out := uitest.ItemsNow(a.view.Lists[i])
	for j, label := range out {
		out[j] = uitest.EntryName(label)
	}
	return out
}

// items returns the labels of the active list.
func (a *app) items() []string {
	var out []string
	a.onLoop(func() { out = a.itemsNow() })
	return out
}

// hasItemNow reports whether the list contains an entry, matched either by its
// raw label or by its plain name; must run on the event loop.
func (a *app) hasItemNow(label string) bool {
	for _, it := range a.itemsNow() {
		if it == label || uitest.EntryName(it) == uitest.EntryName(label) {
			return true
		}
	}
	return false
}

// selectItem moves the cursor onto the list entry with the given name.
func (a *app) selectItem(label string) {
	a.t.Helper()
	a.waitFor("list entry "+label, func() bool {
		for i := 0; i < a.view.List.GetItemCount(); i++ {
			main, _ := a.view.List.GetItemText(i)
			if uitest.EntryName(main) == label {
				a.view.List.SetCurrentItem(i)
				return true
			}
		}
		return false
	})
	a.settle()
}

// currentItemNow returns the entry under the cursor; must run on the loop.
func (a *app) currentItemNow() string {
	main, _ := a.view.List.GetItemText(a.view.List.GetCurrentItem())
	return uitest.EntryName(main)
}

func (a *app) currentItem() string {
	var out string
	a.onLoop(func() { out = a.currentItemNow() })
	return out
}

// waitForCursor waits until the cursor rests on the named entry. Navigation is
// asynchronous, so the cursor moves a moment after the listing arrives.
func (a *app) waitForCursor(name string) {
	a.t.Helper()
	a.waitFor("the cursor on "+name, func() bool { return a.currentItemNow() == name })
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
