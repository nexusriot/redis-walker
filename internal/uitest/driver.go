// Package uitest drives a tview application on a simulated terminal. Both the
// controller tests and the end-to-end suite use it, so the two only differ in
// what they assert, not in how they talk to the UI.
package uitest

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Timeout bounds every wait performed by the driver.
const Timeout = 20 * time.Second

// Driver runs an application and injects key presses into it.
type Driver struct {
	T      *testing.T
	App    *tview.Application
	Screen tcell.SimulationScreen
	Done   chan error
}

// New attaches a simulation screen to an application. Call Start to run it.
func New(t *testing.T, app *tview.Application, width, height int) *Driver {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	app.SetScreen(screen)
	screen.SetSize(width, height)
	return &Driver{T: t, App: app, Screen: screen, Done: make(chan error, 1)}
}

// Start runs the application loop in the background and stops it on cleanup.
func (d *Driver) Start(run func() error) {
	d.T.Helper()
	go func() { d.Done <- run() }()
	d.T.Cleanup(func() {
		d.App.Stop()
		select {
		case <-d.Done:
		case <-time.After(Timeout):
			d.T.Error("the application did not stop")
		}
	})
}

// OnLoop runs f on the event loop and waits for it. It must only be called from
// the test goroutine: calling it inside another queued closure deadlocks.
func (d *Driver) OnLoop(f func()) {
	d.T.Helper()
	done := make(chan struct{})
	d.App.QueueUpdateDraw(func() {
		defer close(done)
		f()
	})
	select {
	case <-done:
	case err := <-d.Done:
		d.T.Fatalf("the application stopped early: %v", err)
	case <-time.After(Timeout):
		d.T.Fatal("timed out waiting for the UI")
	}
}

// Sync waits until every queued update has been drawn.
func (d *Driver) Sync() { d.OnLoop(func() {}) }

// Settle gives the event loop time to consume injected key events.
func (d *Driver) Settle() {
	d.Sync()
	d.Sync()
}

// Key injects a key press without waiting.
func (d *Driver) Key(k tcell.Key, r rune) {
	d.Screen.InjectKey(k, r, tcell.ModNone)
}

// Type injects a string one rune at a time.
func (d *Driver) Type(s string) {
	for _, r := range s {
		d.Key(tcell.KeyRune, r)
	}
	d.Settle()
}

// WaitFor polls a condition on the event loop. cond must only use "...Now"
// readers, because it already runs on the loop.
func (d *Driver) WaitFor(what string, cond func() bool) {
	d.T.Helper()
	deadline := time.Now().Add(Timeout)
	for time.Now().Before(deadline) {
		ok := false
		d.OnLoop(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	d.T.Fatalf("timed out waiting for %s\nscreen:\n%s", what, d.ScreenText())
}

// ScreenTextNow renders the screen; it must run on the event loop.
func (d *Driver) ScreenTextNow() string {
	var sb strings.Builder
	cells, w, h := d.Screen.GetContents()
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

// ScreenText renders the screen from the test goroutine.
func (d *Driver) ScreenText() string {
	var out string
	d.OnLoop(func() { out = d.ScreenTextNow() })
	return out
}

// ItemsNow returns the labels of a list; it must run on the event loop.
func ItemsNow(list *tview.List) []string {
	out := make([]string, 0, list.GetItemCount())
	for i := 0; i < list.GetItemCount(); i++ {
		main, _ := list.GetItemText(i)
		out = append(out, main)
	}
	return out
}

var (
	// styleTag matches a real tview style tag such as "[blue]", "[::b]" or
	// "[-]". It deliberately matches neither the empty "[]" nor the "[" of an
	// escaped tag, so "cache[1[]" survives.
	styleTag = regexp.MustCompile(`\[[a-zA-Z0-9#:_-]+\]`)
	// escapedTag matches tview's escape form "[something[]".
	escapedTag = regexp.MustCompile(`\[([^\[\]]*)\[\]`)
)

// EntryName reduces a list label to the plain name of the entry:
//
//	"   ahash [blue](hash)[-]" -> "ahash"
//	"📁 cache[1[]/"            -> "cache[1]/"
func EntryName(label string) string {
	// Remove the real tags first; only then undo the escaping, otherwise a key
	// name such as "cache[1]" would be stripped as if it were a tag.
	name := styleTag.ReplaceAllString(label, "")
	name = escapedTag.ReplaceAllString(name, "[$1]")
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "📁"))
	if base, _, ok := strings.Cut(name, " ("); ok {
		name = strings.TrimSpace(base)
	}
	return name
}
