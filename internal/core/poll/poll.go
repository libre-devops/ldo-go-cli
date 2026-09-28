// Package poll runs a check repeatedly until it is complete or a limit is reached.
//
// The clock and the sleep are injected, so a poll of hours runs in microseconds in tests.
// The poller never sleeps past the timeout: the last wait is cut short so one final pass
// runs at the deadline.
package poll

import (
	"context"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Limits are how often to check, and when to give up; zero is no limit of that kind.
type Limits struct {
	Interval  time.Duration
	Timeout   time.Duration
	MaxPasses int
}

// Check is an Input error when the limits cannot be polled with.
func (l Limits) Check() error {
	switch {
	case l.Interval <= 0:
		return errs.Inputf("the poll interval must be more than zero seconds")
	case l.Timeout < 0:
		return errs.Inputf("the poll timeout must be more than zero seconds")
	case l.MaxPasses < 0:
		return errs.Inputf("the poll needs at least one pass")
	}
	return nil
}

// Reason is why a poll stopped.
type Reason string

// The reasons.
const (
	Complete  Reason = "complete"
	Timeout   Reason = "timeout"
	MaxPasses Reason = "max-passes"
)

// Outcome is what the poll ended with: the last pass's result and why it stopped.
type Outcome[T any] struct {
	Result  T
	Passes  int
	Elapsed time.Duration
	Reason  Reason
}

// Complete reports whether polling stopped because the condition was met.
func (o Outcome[T]) Complete() bool { return o.Reason == Complete }

// Clock is now, and a wait that ends early when the context is cancelled.
type Clock struct {
	Now   func() time.Time
	Sleep func(ctx context.Context, wait time.Duration) error
}

// RealClock is the system's clock.
var RealClock = Clock{Now: time.Now, Sleep: Sleep}

// Sleep waits, or returns the context's error when it is cancelled first.
func Sleep(ctx context.Context, wait time.Duration) error {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Hooks are told of each pass's result and of each wait.
type Hooks[T any] struct {
	OnPass func(number int, result T)
	OnWait func(wait time.Duration)
}

// Run calls pass(n) for n = 1, 2, ... until complete says so or a limit stops it. An
// error from a pass ends the poll: the pass decides which failures it absorbs (a
// throttled request) and which end it (a rejected token). A cancelled context ends a wait.
func Run[T any](ctx context.Context, pass func(int) (T, error), complete func(T) bool, limits Limits, clock Clock,
	hooks Hooks[T]) (Outcome[T], error) {
	if err := limits.Check(); err != nil {
		return Outcome[T]{}, err
	}
	started := clock.Now()
	for passes := 1; ; passes++ {
		result, err := pass(passes)
		if err != nil {
			return Outcome[T]{Result: result, Passes: passes}, err
		}
		if hooks.OnPass != nil {
			hooks.OnPass(passes, result)
		}
		elapsed := clock.Now().Sub(started)
		outcome := Outcome[T]{Result: result, Passes: passes, Elapsed: elapsed}
		switch {
		case complete(result):
			outcome.Reason = Complete
			return outcome, nil
		case limits.MaxPasses > 0 && passes >= limits.MaxPasses:
			outcome.Reason = MaxPasses
			return outcome, nil
		case limits.Timeout > 0 && elapsed >= limits.Timeout:
			outcome.Reason = Timeout
			return outcome, nil
		}
		wait := limits.Interval
		if limits.Timeout > 0 {
			wait = min(wait, limits.Timeout-elapsed)
		}
		if hooks.OnWait != nil {
			hooks.OnWait(wait)
		}
		if err := clock.Sleep(ctx, wait); err != nil {
			return outcome, err
		}
	}
}
