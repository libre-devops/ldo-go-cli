package poll

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/clockfake"
)

func clock() (*clockfake.Clock, Clock) {
	fake := clockfake.New(time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
	return fake, Clock{Now: fake.Now, Sleep: fake.Sleep}
}

func counter(doneAt int) func(int) (int, error) {
	return func(pass int) (int, error) { return pass, nil }
}

func TestItStopsWhenComplete(t *testing.T) {
	_, fake := clock()
	var seen []int
	outcome, err := Run(context.Background(), counter(3), func(n int) bool { return n == 3 }, Limits{Interval: time.Minute}, fake,
		Hooks[int]{OnPass: func(number, _ int) { seen = append(seen, number) }})
	if err != nil || !outcome.Complete() || outcome.Passes != 3 || outcome.Elapsed != 2*time.Minute || len(seen) != 3 {
		t.Errorf("%+v %v", outcome, err)
	}
}

func TestTheLastWaitIsCutShortAtTheTimeout(t *testing.T) {
	sleeper, fake := clock()
	var waits []time.Duration
	outcome, err := Run(context.Background(), counter(0), func(int) bool { return false },
		Limits{Interval: 4 * time.Minute, Timeout: 10 * time.Minute}, fake, Hooks[int]{OnWait: func(wait time.Duration) { waits = append(waits, wait) }})
	if err != nil || outcome.Reason != Timeout || outcome.Passes != 4 || outcome.Elapsed != 10*time.Minute {
		t.Errorf("%+v %v", outcome, err)
	}
	if len(waits) != 3 || waits[2] != 2*time.Minute || len(sleeper.Slept()) != 3 {
		t.Errorf("%v", waits)
	}
}

func TestMaxPassesErrorsAndCancelling(t *testing.T) {
	_, fake := clock()
	outcome, _ := Run(context.Background(), counter(0), func(int) bool { return false }, Limits{Interval: time.Second, MaxPasses: 2}, fake, Hooks[int]{})
	if outcome.Reason != MaxPasses || outcome.Passes != 2 {
		t.Errorf("%+v", outcome)
	}
	boom := errors.New("boom")
	if _, err := Run(context.Background(), func(int) (int, error) { return 0, boom }, func(int) bool { return false },
		Limits{Interval: time.Second}, fake, Hooks[int]{}); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, counter(0), func(int) bool { return false }, Limits{Interval: time.Second}, fake, Hooks[int]{}); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
	for _, limits := range []Limits{{}, {Interval: time.Second, Timeout: -1}, {Interval: time.Second, MaxPasses: -1}} {
		if _, err := Run(context.Background(), counter(0), func(int) bool { return true }, limits, fake, Hooks[int]{}); !errs.Is(err, errs.Input) {
			t.Errorf("%+v: %v", limits, err)
		}
	}
}

func TestTheRealSleepEndsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Error(err)
	}
	if err := RealClock.Sleep(context.Background(), time.Microsecond); err != nil {
		t.Error(err)
	}
}
