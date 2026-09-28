package sheets

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

// Excel keeps a date as a number: the days since its epoch, with the time of day as the
// fraction. Only the cell's number format makes 46290 look like 25/09/2026, so a reader
// that wants what a person sees reads the format too. These give the day, the time or
// both as ISO text (2026-09-25, 09:00:00, 2026-09-25T09:00:00), whatever the format
// showed, so the same date reads the same from any workbook, whichever country saved it.
//
// Two quirks are Excel's own: most workbooks count from 1900 and some old Mac ones from
// 1904, and the 1900 count has a 29 February 1900 that never was (kept for Lotus 1-2-3).

// DateKind is what a number format shows: "date", "time", "datetime", or "" for neither.
type DateKind string

// The kinds of date a format shows.
const (
	Date     DateKind = "date"
	Time     DateKind = "time"
	DateTime DateKind = "datetime"
)

// builtInFormats are the built-in formats (ECMA-376 part 1, 18.8.30) that show a date or
// a time, by id. 46 ([h]:mm:ss) is an elapsed time, a length of time rather than a
// moment, so it is not one.
var builtInFormats = func() map[int]DateKind {
	found := map[int]DateKind{22: DateTime}
	for _, id := range []int{14, 15, 16, 17} {
		found[id] = Date
	}
	for _, id := range []int{18, 19, 20, 21, 45, 47} {
		found[id] = Time
	}
	// The East Asian date formats, which Excel numbers apart.
	for id := 27; id <= 36; id++ {
		found[id] = Date
	}
	for id := 50; id <= 58; id++ {
		found[id] = Date
	}
	return found
}()

// In a format code: literal text, an escaped character, and padding, none of which is a
// date part however it is spelled.
var (
	literal   = regexp.MustCompile(`"[^"]*"|\\.|_.|\*.`)
	bracketed = regexp.MustCompile(`\[([^\]]*)\]`)
	elapsed   = regexp.MustCompile(`^[hmsHMS]+$`)
)

const phantomDay = 60

// FormatKind is what a cell with this number format shows. code is the format's code
// when the workbook defines it (ids from 164 on); a built-in format is known by its id.
func FormatKind(formatID int, code *string) DateKind {
	if code == nil {
		return builtInFormats[formatID]
	}
	// The first section is the one for positive numbers, which dates are.
	first, _, _ := strings.Cut(*code, ";")
	section := literal.ReplaceAllString(first, "")
	for _, match := range bracketed.FindAllStringSubmatch(section, -1) {
		if elapsed.MatchString(match[1]) {
			return ""
		}
	}
	section = strings.ToLower(bracketed.ReplaceAllString(section, "")) // colours, locales, conditions
	hasTime := strings.ContainsAny(section, "hs") || strings.Contains(section, "am/pm") || strings.Contains(section, "a/p")
	section = strings.ReplaceAll(strings.ReplaceAll(section, "am/pm", ""), "a/p", "")
	// m alone is a month (mmm yyyy); beside an h or an s it is a minute (hh:mm).
	hasDate := strings.ContainsAny(section, "yd") || (strings.Contains(section, "m") && !hasTime)
	switch {
	case hasDate && hasTime:
		return DateTime
	case hasDate:
		return Date
	case hasTime:
		return Time
	}
	return ""
}

// FromSerial is a cell's number as the ISO date, time or both its format shows, or ""
// when it is no day there was (negative, too large, or the 29 February 1900 Excel counts).
func FromSerial(serial float64, kind DateKind, date1904 bool) string {
	if math.IsNaN(serial) || math.IsInf(serial, 0) || serial < 0 || serial > 3e6 {
		return ""
	}
	total := int64(math.RoundToEven(serial * 86400))
	days, seconds := total/86400, total%86400
	moment := fmt.Sprintf("%02d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	if kind == Time {
		return moment
	}
	day, ok := serialDay(days, date1904)
	if !ok {
		return ""
	}
	if kind == Date {
		return day
	}
	return day + "T" + moment
}

func serialDay(days int64, date1904 bool) (string, bool) {
	var epoch time.Time
	switch {
	case date1904:
		epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case days == phantomDay:
		return "", false
	case days < phantomDay:
		// The days before the 29 February that never was.
		epoch = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	default:
		epoch = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	day := epoch.AddDate(0, 0, int(days))
	if day.Year() > 9999 {
		return "", false
	}
	return day.Format("2006-01-02"), true
}
