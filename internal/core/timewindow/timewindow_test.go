package timewindow

import (
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

var london, _ = time.LoadLocation("Europe/London")
var now = time.Date(2026, 9, 24, 15, 30, 0, 0, london)

func choose(t *testing.T, choice Choice) Window {
	t.Helper()
	window, err := Choose(choice, now)
	if err != nil {
		t.Fatal(err)
	}
	return window
}

func TestDaysAreLocalDaysWhole(t *testing.T) {
	today := choose(t, Choice{Today: true})
	if today.Start != time.Date(2026, 9, 24, 0, 0, 0, 0, london) || today.End != time.Date(2026, 9, 25, 0, 0, 0, 0, london) || today.Label != "today" {
		t.Errorf("%+v", today)
	}
	yesterday := choose(t, Choice{Yesterday: true})
	if yesterday.Start.Day() != 23 || yesterday.Label != "yesterday" {
		t.Errorf("%+v", yesterday)
	}
	span := choose(t, Choice{From: "2026-09-01", To: "2026-09-24"})
	if span.Start.Day() != 1 || span.End.Day() != 25 || span.Label != "2026-09-01 to 2026-09-24" {
		t.Errorf("%+v", span)
	}
	one := choose(t, Choice{From: "2026-09-20", To: "2026-09-20"})
	if one.Label != "2026-09-20" || one.End.Sub(one.Start) != 24*time.Hour {
		t.Errorf("%+v", one)
	}
}

func TestMomentsAreLocalUnlessTheySayOtherwise(t *testing.T) {
	local := choose(t, Choice{From: "2026-09-24T09:00", To: "2026-09-24 12:30"})
	if local.Start != time.Date(2026, 9, 24, 9, 0, 0, 0, london) || local.End.Sub(local.Start) != 210*time.Minute ||
		local.Label != "2026-09-24 09:00 to 2026-09-24 12:30" {
		t.Errorf("%+v", local)
	}
	utc := choose(t, Choice{From: "2026-09-24t08:00z"})
	if !utc.Start.Equal(time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)) || utc.Label != "from 2026-09-24 08:00Z" || !utc.End.IsZero() {
		t.Errorf("%+v", utc)
	}
	offset := choose(t, Choice{To: "2026-09-24T10:00:30+02:00"})
	if !offset.End.Equal(time.Date(2026, 9, 24, 8, 0, 30, 0, time.UTC)) || offset.Label != "up to 2026-09-24 10:00:30+02:00" {
		t.Errorf("%+v", offset)
	}
}

func TestSpansDefaultsAndMistakes(t *testing.T) {
	last := choose(t, Choice{Since: 6 * time.Hour})
	if last.Start != now.Add(-6*time.Hour) || last.Label != "the last 6h" {
		t.Errorf("%+v", last)
	}
	if all := choose(t, Choice{}); all.Label != "all time" || !all.Start.IsZero() {
		t.Errorf("%+v", all)
	}
	if fallback := choose(t, Choice{Default: Today}); fallback.Label != "today" {
		t.Errorf("%+v", fallback)
	}
	for _, choice := range []Choice{{Today: true, Since: time.Hour}, {From: "tomorrow"}, {From: "2026-09-24", To: "2026-09-01"},
		{From: "2026-02-30"}, {To: "2026-09-24T25:00"}} {
		if _, err := Choose(choice, now); !errs.Is(err, errs.Input) {
			t.Errorf("%+v: %v", choice, err)
		}
	}
}

func TestContains(t *testing.T) {
	window := DayWindow(time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC), "")
	if window.Label != "2026-09-24" || !window.Contains(window.Start) || window.Contains(window.End) || window.Contains(time.Time{}) {
		t.Errorf("%+v", window)
	}
	if (Window{}).Contains(now) != true {
		t.Error("an open window holds everything")
	}
}
