// Package clockfake is a clock for tests: it stands still until something sleeps, and a
// sleep moves it on at once, so a watch of hours runs in microseconds.
package clockfake

import (
	"context"
	"sync"
	"time"
)

// Clock is a fake clock at Current.
type Clock struct {
	mu      sync.Mutex
	current time.Time
	slept   []time.Duration
}

// New is a clock at start.
func New(start time.Time) *Clock { return &Clock{current: start} }

// Now is the clock's time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// Advance moves the clock on.
func (c *Clock) Advance(by time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current = c.current.Add(by)
}

// Sleep moves the clock on by wait, at once, unless ctx is already cancelled.
func (c *Clock) Sleep(ctx context.Context, wait time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.slept = append(c.slept, wait)
	c.current = c.current.Add(wait)
	c.mu.Unlock()
	return nil
}

// Slept is every wait asked for, in order.
func (c *Clock) Slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.slept...)
}
