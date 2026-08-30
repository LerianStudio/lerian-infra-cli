package main

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// Replacing a live spinner used to deadlock, and the deadlock was invisible in a
// test because the spinner is inert off-terminal: Update holds c.mu, the spinner is
// handed &c.mu, its painting goroutine takes that mutex every tick, and Stop waits
// for that goroutine and then takes the mutex itself. Nothing could ever finish.
//
// This drives the real thing — a live spinner sharing a real mutex — and fails by
// timing out rather than hanging the suite.
func TestReplacingALiveSpinnerDoesNotDeadlock(t *testing.T) {
	out := &bytes.Buffer{}

	c := &checklist{out: out, index: map[string]int{}}
	// A live spinner: newSpinner returns an inert one for a non-terminal
	// destination, so it is built by hand to reproduce the terminal path.
	c.spin = &spinner{
		// The SAME mutex the checklist holds: that sharing is the deadlock.
		mu: &c.mu, out: io.Discard, label: "running",
		started: time.Now(), frames: spinnerFrames(),
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	go c.spin.run()

	done := make(chan struct{})
	go func() {
		c.mu.Lock()
		c.stopSpinner()
		c.mu.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stopSpinner deadlocked: it must release c.mu around Stop")
	}
	if c.spin != nil {
		t.Error("the spinner should be cleared after stopping")
	}
}
