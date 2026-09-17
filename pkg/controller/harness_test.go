package controller

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gdamore/tcell/v2"

	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
)

// harness runs the real tview application on a simulation screen so that the
// controller can be driven exactly as it behaves in a terminal.
type harness struct {
	t      *testing.T
	mr     *miniredis.Miniredis
	m      *model.Model
	v      *view.View
	c      *Controller
	screen tcell.SimulationScreen
	done   chan error
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

	v := view.NewView()
	v.SetHeader("test")
	screen := tcell.NewSimulationScreen("UTF-8")
	v.App.SetScreen(screen)
	screen.SetSize(140, 40)

	h := &harness{
		t: t, mr: mr, m: m, v: v,
		c:      New(m, v, false),
		screen: screen,
		done:   make(chan error, 1),
	}
	go func() { h.done <- h.c.Run() }()

	t.Cleanup(func() {
		h.v.App.Stop()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			t.Error("the application did not stop")
		}
		_ = m.Close()
	})

	h.sync()
	return h
}

// do runs f on the tview event loop and waits for it to finish.
func (h *harness) do(f func()) {
	h.t.Helper()
	done := make(chan struct{})
	h.v.App.QueueUpdateDraw(func() {
		defer close(done)
		f()
	})
	select {
	case <-done:
	case err := <-h.done:
		h.t.Fatalf("the application stopped early: %v", err)
	case <-time.After(5 * time.Second):
		h.t.Fatal("timed out waiting for the event loop")
	}
}

// sync waits for every queued update to be processed.
func (h *harness) sync() { h.do(func() {}) }

// waitFor polls a condition on the event loop.
func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := false
		h.do(func() { ok = cond() })
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

// key injects a real key press into the screen.
func (h *harness) key(k tcell.Key, r rune) {
	h.screen.InjectKey(k, r, tcell.ModNone)
}

// items returns the labels currently shown in the list.
func (h *harness) items() []string {
	var out []string
	h.do(func() {
		for i := 0; i < h.v.List.GetItemCount(); i++ {
			main, _ := h.v.List.GetItemText(i)
			out = append(out, main)
		}
	})
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
	h.do(func() { name, _ = h.v.Pages.GetFrontPage() })
	return name
}

// details returns the text of the details pane.
func (h *harness) details() string {
	var out string
	h.do(func() { out = h.v.Details.GetText(true) })
	return out
}

// screenText renders the simulation screen as plain text.
func (h *harness) screenText() string {
	var sb strings.Builder
	h.do(func() {
		cells, w, hgt := h.screen.GetContents()
		for y := 0; y < hgt; y++ {
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
	})
	return sb.String()
}
