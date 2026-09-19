package controller

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gdamore/tcell/v2"

	"github.com/nexusriot/redis-walker/internal/uitest"
	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
)

// harness runs the real tview application on a simulation screen so that the
// controller can be driven exactly as it behaves in a terminal.
type harness struct {
	*uitest.Driver
	t  *testing.T
	mr *miniredis.Miniredis
	v  *view.View
	c  *Controller
}

func newHarness(t *testing.T, seed map[string]string, opts ...func(*model.Options)) *harness {
	t.Helper()

	mr := miniredis.RunT(t)
	for k, v := range seed {
		mr.Set(k, v)
	}

	o := model.Options{Host: mr.Host(), Port: mr.Port()}
	for _, f := range opts {
		f(&o)
	}
	m, err := model.New(o)
	if err != nil {
		t.Fatalf("model.New: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })

	v := view.NewView()
	v.SetHeader("test")

	h := &harness{t: t, mr: mr, v: v, c: New(m, v, false)}
	h.Driver = uitest.New(t, v.App, 140, 40)
	h.Start(h.c.Run)

	h.Sync()
	h.waitIdle()
	return h
}

// do runs f on the event loop.
func (h *harness) do(f func()) { h.OnLoop(f) }

// waitFor polls a condition on the event loop.
func (h *harness) waitFor(what string, cond func() bool) { h.WaitFor(what, cond) }

// waitIdle blocks until no background operation is running.
func (h *harness) waitIdle() {
	h.t.Helper()
	h.waitFor("the controller to become idle", func() bool { return h.c.Idle() })
}

// act runs f on the event loop and waits for the work it started.
func (h *harness) act(f func()) {
	h.t.Helper()
	h.do(f)
	h.waitIdle()
}

// key injects a key press without waiting for the work it starts.
func (h *harness) key(k tcell.Key, r rune) { h.Key(k, r) }

// press injects a key and waits for the work it started.
func (h *harness) press(k tcell.Key) {
	h.t.Helper()
	h.key(k, 0)
	h.Settle()
	h.waitIdle()
}

// node returns a node of the active pane by its map key.
func (h *harness) node(mapKey string) *model.Node {
	h.t.Helper()
	var n *model.Node
	h.do(func() { n = h.c.cur().nodes[mapKey] })
	if n == nil {
		h.t.Fatalf("no node %q in %v", mapKey, h.items())
	}
	return n
}

// items returns the labels currently shown in the active list.
func (h *harness) items() []string {
	var out []string
	h.do(func() { out = uitest.ItemsNow(h.v.List) })
	return out
}

// currentItem returns the label under the cursor.
func (h *harness) currentItem() string {
	var out string
	h.do(func() {
		main, _ := h.v.List.GetItemText(h.v.List.GetCurrentItem())
		out = main
	})
	return out
}

// frontPage returns the name of the topmost page.
func (h *harness) frontPage() string {
	var name string
	h.do(func() { name = h.frontPageNow() })
	return name
}

// frontPageNow returns the topmost page; it must run on the event loop.
func (h *harness) frontPageNow() string {
	name, _ := h.v.Pages.GetFrontPage()
	return name
}

// details returns the text of the details pane.
func (h *harness) details() string {
	var out string
	h.do(func() { out = h.v.Details.GetText(true) })
	return out
}

// status returns the text of the bottom status line.
func (h *harness) status() string {
	var out string
	h.do(func() { out = h.v.Status.GetText(true) })
	return out
}

// screenText renders the simulation screen as plain text.
func (h *harness) screenText() string { return h.ScreenText() }

// gzipBytes compresses a string for the value-decoding tests.
func gzipBytes(t *testing.T, s string) string {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
