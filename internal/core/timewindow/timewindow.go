// Package timewindow is time windows for "what happened when": today, yesterday, the last
// 7d, or between two ends.
//
// Days are local days: "today" starts at midnight where you are, and --from 2026-09-01
// --to 2026-09-24 covers both days whole. An end can be a moment instead, a day and a
// time: --from 2026-09-24T09:00 --to 2026-09-24T12:30 is those three and a half hours,
// local time, or UTC with a Z (2026-09-24T09:00Z) or another offset.
package timewindow

import (
	"regexp"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

var (
	day    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	moment = regexp.MustCompile(`^(?i)(\d{4}-\d{2}-\d{2})[T ](\d{2}:\d{2})(:\d{2})?(Z|[+-]\d{2}:\d{2})?$`)
)

// Window is from Start (inclusive) to End (exclusive); a zero time is open-ended.
type Window struct {
	Start time.Time
	End   time.Time
	Label string
}

// Contains reports whether when falls in the window: from its start, up to but not
// including its end.
func (w Window) Contains(when time.Time) bool {
	if when.IsZero() || (!w.Start.IsZero() && when.Before(w.Start)) {
		return false
	}
	return w.End.IsZero() || when.Before(w.End)
}

// ParseDay is today, yesterday or YYYY-MM-DD, as a midnight in today's location.
func ParseDay(text string, today time.Time) (time.Time, error) {
	value := strings.ToLower(strings.TrimSpace(text))
	switch value {
	case "today":
		return Midnight(today), nil
	case "yesterday":
		return Midnight(today).AddDate(0, 0, -1), nil
	}
	if day.MatchString(value) {
		if parsed, err := time.ParseInLocation("2006-01-02", value, today.Location()); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errs.Inputf("'%s' is not a day", text).WithHint("use YYYY-MM-DD, today or yesterday, or a time too")
}

// Midnight is the start of when's day, in its location.
func Midnight(when time.Time) time.Time {
	return time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, when.Location())
}

// Choice is the options that name a window: only one kind may be given.
type Choice struct {
	Today     bool
	Yesterday bool
	Since     time.Duration
	From      string
	To        string
	// Default is the window when none is named; all time when nil.
	Default func(now time.Time) Window
}

// Choose is the window the options name, at now (whose location is the local zone).
func Choose(choice Choice, now time.Time) (Window, error) {
	kinds := 0
	for _, given := range []bool{choice.Today, choice.Yesterday, choice.Since != 0, choice.From != "" || choice.To != ""} {
		if given {
			kinds++
		}
	}
	switch {
	case kinds > 1:
		return Window{}, errs.Inputf("choose one time window").WithHint("--today, --yesterday, --since, or --from and --to")
	case choice.Today:
		return DayWindow(Midnight(now), "today"), nil
	case choice.Yesterday:
		return DayWindow(Midnight(now).AddDate(0, 0, -1), "yesterday"), nil
	case choice.Since != 0:
		return Last(choice.Since, now), nil
	case choice.From != "" || choice.To != "":
		return between(choice.From, choice.To, now)
	case choice.Default != nil:
		return choice.Default(now), nil
	}
	return Window{Label: "all time"}, nil
}

type end struct {
	at    time.Time
	label string
}

func between(from, to string, now time.Time) (Window, error) {
	var first, final *end
	if from != "" {
		found, err := endOf(from, now, false)
		if err != nil {
			return Window{}, err
		}
		first = &found
	}
	if to != "" {
		found, err := endOf(to, now, true)
		if err != nil {
			return Window{}, err
		}
		final = &found
	}
	switch {
	case first != nil && final != nil:
		if !first.at.Before(final.at) {
			return Window{}, errs.Inputf("--from %s is after --to %s", first.label, final.label)
		}
		// One label when both ends are the same day: --from 2026-09-20 --to 2026-09-20.
		label := first.label
		if final.label != first.label {
			label += " to " + final.label
		}
		return Window{Start: first.at, End: final.at, Label: label}, nil
	case first != nil:
		return Window{Start: first.at, Label: "from " + first.label}, nil
	}
	return Window{End: final.at, Label: "up to " + final.label}, nil
}

// endOf is a moment as it is (local time unless it says otherwise), or a day whole: from
// its midnight, or, as the closing end, up to the next.
func endOf(text string, now time.Time, closing bool) (end, error) {
	value := strings.TrimSpace(text)
	if match := moment.FindStringSubmatch(value); match != nil {
		clock := match[2] + match[3]
		if match[3] == "" {
			clock += ":00"
		}
		layout, stamp := "2006-01-02T15:04:05", match[1]+"T"+clock
		zone := strings.ToUpper(match[4])
		var parsed time.Time
		var err error
		if zone == "" {
			parsed, err = time.ParseInLocation(layout, stamp, now.Location())
		} else {
			parsed, err = time.Parse(layout+"Z07:00", stamp+zone)
		}
		if err != nil {
			return end{}, errs.Inputf("'%s' is not a time", text).WithHint("use YYYY-MM-DDTHH:MM")
		}
		// As typed, but always the same way: 2026-09-24 09:00, or 2026-09-24 08:00Z.
		return end{at: parsed, label: strings.Replace(strings.ToUpper(value), "T", " ", 1)}, nil
	}
	found, err := ParseDay(value, now)
	if err != nil {
		return end{}, err
	}
	label := found.Format("2006-01-02")
	if closing {
		found = found.AddDate(0, 0, 1)
	}
	return end{at: found, label: label}, nil
}

// DayWindow is the whole of the day starting at midnight.
func DayWindow(midnight time.Time, label string) Window {
	if label == "" {
		label = midnight.Format("2006-01-02")
	}
	return Window{Start: midnight, End: midnight.AddDate(0, 0, 1), Label: label}
}

// Last is the span up to now.
func Last(span time.Duration, now time.Time) Window {
	return Window{Start: now.Add(-span), Label: "the last " + util.FormatSpan(span)}
}

// Today is from midnight today, local time, with no end.
func Today(now time.Time) Window {
	return Window{Start: Midnight(now), Label: "today"}
}
