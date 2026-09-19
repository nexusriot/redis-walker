package controller

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// spinnerFrames animates the status line while an operation runs.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// progressInterval throttles status updates so that a fast scan cannot flood
// the tview update queue.
const progressInterval = 80 * time.Millisecond

// job is the single operation a controller may have in flight.
type job struct {
	desc   string
	detail string
	cancel context.CancelFunc
	frame  int
}

// Idle reports whether no background operation is running.
func (c *Controller) Idle() bool { return c.job == nil }

// Cancel aborts the running operation, if any.
func (c *Controller) Cancel() {
	if c.job == nil {
		return
	}
	c.job.cancel()
	c.view.SetStatus("[yellow]cancelling " + c.job.desc + "…[-]")
}

func (c *Controller) renderStatus() {
	if c.job == nil {
		c.view.SetStatus(c.idleStatus)
		return
	}
	frame := spinnerFrames[c.job.frame%len(spinnerFrames)]
	text := fmt.Sprintf("[yellow]%c[-] %s", frame, c.job.desc)
	if c.job.detail != "" {
		text += "  " + c.job.detail
	}
	c.view.SetStatus(text + "   [::d](Esc to cancel)[::-]")
}

// setIdleStatus sets the text shown when nothing is running.
func (c *Controller) setIdleStatus(text string) {
	c.idleStatus = text
	if c.job == nil {
		c.view.SetStatus(text)
	}
}

// runAsync executes work on a background goroutine and delivers the result on
// the UI goroutine. Only one operation runs at a time; further requests are
// rejected so that the model is never used concurrently.
func runAsync[T any](c *Controller, desc string, work func(ctx context.Context, report func(string)) (T, error), done func(T, error)) bool {
	if c.job != nil {
		c.view.SetStatus("[red]busy:[-] " + c.job.desc + " is still running (Esc cancels it)")
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	j := &job{desc: desc, cancel: cancel}
	c.job = j
	c.renderStatus()
	c.startSpinner(ctx)

	report := func(detail string) {
		c.view.App.QueueUpdateDraw(func() {
			if c.job != j {
				return
			}
			j.detail = detail
			c.renderStatus()
		})
	}

	go func() {
		res, err := work(ctx, report)
		c.view.App.QueueUpdateDraw(func() {
			cancel()
			if c.job == j {
				c.job = nil
			}
			if errors.Is(err, context.Canceled) {
				c.setIdleStatus("[yellow]" + desc + " cancelled[-]")
				return
			}
			c.setIdleStatus("")
			done(res, err)
		})
	}()
	return true
}

func (c *Controller) startSpinner(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.view.App.QueueUpdateDraw(func() {
					if c.job == nil {
						return
					}
					c.job.frame++
					c.renderStatus()
				})
			}
		}
	}()
}

// throttled wraps a progress callback so that it reports at most every
// progressInterval.
func throttled(report func(string), format func(int) string) func(int) {
	var last time.Time
	return func(done int) {
		now := time.Now()
		if now.Sub(last) < progressInterval {
			return
		}
		last = now
		report(format(done))
	}
}
