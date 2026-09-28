// Package rowfilters picks the rows of a CSV or workbook to read names from:
// --where COLUMN=VALUE.
//
//	--where "Scheduled Date=tomorrow" --where "Environment=Dev"
//	--where "Scheduled Date=2026-09-01..2026-09-14"
//	--where "Scheduled Date=last 7d"
//
// A condition is COLUMN=VALUE, or COLUMN!=VALUE to leave rows out. Conditions on one
// column are alternatives (any of its values will do); conditions on different columns
// must all hold. Columns are found by header, and values compared as text, whatever
// their case and the spaces around them.
//
// A value that is a date, or a span of them, is compared as days: today, tomorrow,
// yesterday, 2026-09-25, or a UK or a US date (25/09/2026, 09/25/2026); FROM..TO, both
// included, where either end may be left out; last 7d or next 7d. A day matches a cell
// holding it, with or without a time after it.
//
// UK and US dates differ only when both numbers are 12 or under. Nothing here guesses: a
// span says which it is written in when either end has a number over 12, and so does a
// column once one of its dates has. A value nothing settles, a column that never says,
// and a column holding both are refused, saying why.
package rowfilters

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Order is how slashed dates are written: "uk", "us", or "" when nothing says.
type Order string

// The orders.
const (
	UK Order = "uk"
	US Order = "us"
)

var relative = map[string]int{"yesterday": -1, "today": 0, "tomorrow": 1}

var (
	iso     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?)?$`)
	slashed = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})(?:[T ]\d{1,2}:\d{2}(?::\d{2})?(?:\s?[AaPp][Mm])?)?$`)
	window  = regexp.MustCompile(`^(?i)(last|next)\s*(\d{1,4})d$`)
)

const exampleCount = 5

// SpanHint is how days are written.
const SpanHint = "e.g. 2026-09-01..2026-09-14, ..2026-09-14, today.., last 7d or next 7d"

// Day is a day as written: known already (ISO, or a keyword), or UK or US numbers not yet
// read.
type Day struct {
	Known   time.Time
	Slashed *[3]int
}

func date(year, month, day int) (time.Time, bool) {
	found := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return found, found.Year() == year && int(found.Month()) == month && found.Day() == day
}

// Order is UK or US, when the numbers alone say which.
func (d Day) Order() Order {
	if d.Slashed == nil {
		return ""
	}
	return orderShown(d.Slashed[0], d.Slashed[1])
}

// Read is the day, reading slashed numbers as order when they alone cannot say which;
// false when they cannot, or are no real day.
func (d Day) Read(order Order) (time.Time, bool) {
	if d.Slashed == nil {
		return d.Known, !d.Known.IsZero()
	}
	first, second, year := d.Slashed[0], d.Slashed[1], d.Slashed[2]
	use := d.Order()
	if use == "" {
		use = order
	}
	if use == "" && first == second {
		use = UK
	}
	switch use {
	case UK:
		return date(year, second, first)
	case US:
		return date(year, first, second)
	}
	return time.Time{}, false
}

// Span is the days from Start to End, both included; nil at an end for no limit.
type Span struct {
	Start, End *Day
}

// Order is UK or US, when either end says which.
func (s Span) Order() Order {
	for _, day := range []*Day{s.Start, s.End} {
		if day != nil && day.Order() != "" {
			return day.Order()
		}
	}
	return ""
}

// Condition is one COLUMN=VALUE (or !=). Days is set when the value is a date or a span.
type Condition struct {
	Text    string
	Column  string
	Value   string
	Negated bool
	Days    *Span
}

// DaySpan is the first and last day value names, as a --where date or span would; a zero
// time at an end for no limit.
func DaySpan(value string, today time.Time) (time.Time, time.Time, error) {
	span, err := valueDays(strings.TrimSpace(value), today)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if span == nil {
		return time.Time{}, time.Time{}, errs.Inputf("'%s' is not a day or a span of days", value).WithHint(SpanHint)
	}
	var ends [2]time.Time
	for index, day := range []*Day{span.Start, span.End} {
		if day == nil {
			continue
		}
		read, ok := day.Read(span.Order())
		if !ok {
			return time.Time{}, time.Time{}, errs.Inputf("'%s' could be a UK or a US date", value).
				WithHint("write it as YYYY-MM-DD, e.g. 2026-09-25")
		}
		ends[index] = read
	}
	return ends[0], ends[1], nil
}

// Parse is each COLUMN=VALUE or COLUMN!=VALUE, with today for the relative days.
func Parse(texts []string, today time.Time) ([]Condition, error) {
	var conditions []Condition
	for _, text := range texts {
		condition, err := parseOne(text, today)
		if err != nil {
			return nil, err
		}
		conditions = append(conditions, condition)
	}
	return conditions, nil
}

func parseOne(text string, today time.Time) (Condition, error) {
	at := strings.Index(text, "=")
	negated := at > 0 && text[at-1] == '!'
	column := ""
	if at > 0 {
		end := at
		if negated {
			end--
		}
		column = strings.TrimSpace(text[:end])
	}
	if column == "" {
		return Condition{}, errs.Inputf("--where '%s' is not COLUMN=VALUE", text).
			WithHint(`e.g. --where "Scheduled Date=tomorrow", or "Status!=Done" to leave rows out`)
	}
	value := strings.TrimSpace(text[at+1:])
	days, err := valueDays(value, today)
	if err != nil {
		return Condition{}, err
	}
	return Condition{Text: text, Column: column, Value: value, Negated: negated, Days: days}, nil
}

// Test is a test of a row's cells against every condition.
type Test func(cells []string) bool

type group struct {
	index    int
	negated  bool
	matchers []func(string) bool
}

// RowTest is a test of a row's cells against every condition, for the table with this
// header; rows are all its rows, from which each date column's UK or US order is read.
func RowTest(conditions []Condition, header []string, rows [][]string, source string) (Test, error) {
	var groups []*group
	for _, condition := range conditions {
		index, err := columnIndex(header, condition.Column, source)
		if err != nil {
			return nil, err
		}
		matcher, err := matcherFor(condition, index, rows, source)
		if err != nil {
			return nil, err
		}
		var found *group
		for _, existing := range groups {
			if existing.index == index && existing.negated == condition.Negated {
				found = existing
			}
		}
		if found == nil {
			found = &group{index: index, negated: condition.Negated}
			groups = append(groups, found)
		}
		found.matchers = append(found.matchers, matcher)
	}
	return func(cells []string) bool {
		for _, each := range groups {
			cell := cellAt(cells, each.index)
			matched := false
			for _, match := range each.matchers {
				if match(cell) {
					matched = true
					break
				}
			}
			if matched == each.negated {
				return false
			}
		}
		return true
	}, nil
}

func cellAt(cells []string, index int) string {
	if index < len(cells) {
		return strings.TrimSpace(cells[index])
	}
	return ""
}

// Examples is a few of the values a column holds, for a hint when no row matched.
func Examples(rows [][]string, header []string, column string) string {
	index := -1
	for at, cell := range header {
		if strings.EqualFold(strings.TrimSpace(cell), strings.TrimSpace(column)) {
			index = at
			break
		}
	}
	var seen []string
	for _, cells := range rows {
		value := cellAt(cells, index)
		if value != "" && !slices.Contains(seen, value) {
			seen = append(seen, value)
		}
		if len(seen) == exampleCount {
			break
		}
	}
	if len(seen) == 0 {
		return "nothing"
	}
	return strings.Join(seen, ", ")
}

// WrittenDay is text as a day, when it is written one of the ways read here.
func WrittenDay(text string) (*Day, bool) {
	if match := iso.FindStringSubmatch(text); match != nil {
		found, ok := date(atoi(match[1]), atoi(match[2]), atoi(match[3]))
		if !ok {
			return nil, false
		}
		return &Day{Known: found}, true
	}
	if match := slashed.FindStringSubmatch(text); match != nil {
		return &Day{Slashed: &[3]int{atoi(match[1]), atoi(match[2]), atoi(match[3])}}, true
	}
	return nil, false
}

func atoi(text string) int {
	value, _ := strconv.Atoi(text)
	return value
}

// valueDays is the days a value names: a span, a window, one day, or nil when it is text.
func valueDays(value string, today time.Time) (*Span, error) {
	today = midnight(today)
	if match := window.FindStringSubmatch(value); match != nil {
		return windowSpan(strings.ToLower(match[1]), atoi(match[2]), today, value)
	}
	lowered := strings.ToLower(value)
	if strings.HasPrefix(lowered, "last ") || strings.HasPrefix(lowered, "next ") {
		return nil, errs.Inputf("'%s' is not a span of days", value).WithHint(SpanHint)
	}
	if start, end, found := strings.Cut(value, ".."); found {
		first, err := spanEnd(start, today, value)
		if err != nil {
			return nil, err
		}
		last, err := spanEnd(end, today, value)
		if err != nil {
			return nil, err
		}
		if first == nil && last == nil {
			return nil, errs.Inputf("'%s' names no days", value).WithHint(SpanHint)
		}
		return &Span{Start: first, End: last}, nil
	}
	day, err := dayOf(value, today)
	if err != nil || day == nil {
		return nil, err
	}
	return &Span{Start: day, End: day}, nil
}

func windowSpan(direction string, count int, today time.Time, value string) (*Span, error) {
	if count < 1 {
		return nil, errs.Inputf("'%s' is no days", value).WithHint(SpanHint)
	}
	reach := count - 1
	if direction == "last" {
		return &Span{Start: &Day{Known: today.AddDate(0, 0, -reach)}, End: &Day{Known: today}}, nil
	}
	return &Span{Start: &Day{Known: today}, End: &Day{Known: today.AddDate(0, 0, reach)}}, nil
}

// spanEnd is one end of a span: a day, or nil when left out.
func spanEnd(text string, today time.Time, value string) (*Day, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	day, err := dayOf(text, today)
	if err != nil {
		return nil, err
	}
	if day == nil {
		return nil, errs.Inputf("'%s' in '%s' is not a date", text, value).WithHint(SpanHint)
	}
	return day, nil
}

// dayOf is a keyword or a written date as a day; nil when the value is not written as one.
func dayOf(value string, today time.Time) (*Day, error) {
	if offset, ok := relative[strings.ToLower(value)]; ok {
		return &Day{Known: today.AddDate(0, 0, offset)}, nil
	}
	if !iso.MatchString(value) && !slashed.MatchString(value) {
		return nil, nil
	}
	day, ok := WrittenDay(value)
	if ok && day.Slashed != nil {
		_, asUK := day.Read(UK)
		_, asUS := day.Read(US)
		ok = asUK || asUS
	}
	if !ok {
		return nil, errs.Inputf("'%s' is not a date", value).WithHint("write it as YYYY-MM-DD, e.g. 2026-09-25")
	}
	return day, nil
}

func midnight(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
}

func orderShown(first, second int) Order {
	switch {
	case first > 12:
		return UK
	case second > 12:
		return US
	}
	return ""
}

func columnIndex(header []string, column, source string) (int, error) {
	var named []string
	for index, cell := range header {
		if strings.EqualFold(strings.TrimSpace(cell), column) {
			return index, nil
		}
		if strings.TrimSpace(cell) != "" {
			named = append(named, cell)
		}
	}
	return 0, errs.Inputf("%s has no column '%s' to filter on", source, column).WithHint("columns: %s", strings.Join(named, ", "))
}

func matcherFor(condition Condition, index int, rows [][]string, source string) (func(string) bool, error) {
	if condition.Days == nil {
		return func(cell string) bool { return strings.EqualFold(cell, condition.Value) }, nil
	}
	order, err := columnOrder(rows, index, condition.Column, source)
	if err != nil {
		return nil, err
	}
	// A span written one way (01/09/2026..14/09/2026) reads both its ends that way.
	reading := condition.Days.Order()
	if reading == "" {
		reading = order
	}
	start, err := readEnd(condition.Days.Start, reading, condition)
	if err != nil {
		return nil, err
	}
	end, err := readEnd(condition.Days.End, reading, condition)
	if err != nil {
		return nil, err
	}
	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return nil, errs.Inputf("'%s' ends before it starts", condition.Value).WithHint(SpanHint)
	}
	return func(cell string) bool {
		written, ok := WrittenDay(cell)
		if !ok {
			return false
		}
		day, ok := written.Read(order)
		return ok && (start.IsZero() || !day.Before(start)) && (end.IsZero() || !day.After(end))
	}, nil
}

func readEnd(day *Day, order Order, condition Condition) (time.Time, error) {
	if day == nil {
		return time.Time{}, nil
	}
	read, ok := day.Read(order)
	if !ok {
		return time.Time{}, errs.Inputf("'%s' could be a UK or a US date, and the column '%s' does not say which",
			condition.Value, condition.Column).WithHint("%s", bothWays(*day))
	}
	return read, nil
}

// columnOrder is whether the column's slashed dates are UK or US, as the first over 12
// says; "" when it holds none. A column holding both, or never saying, is refused.
func columnOrder(rows [][]string, index int, column, source string) (Order, error) {
	shown := map[Order]string{}
	unsure := ""
	for _, cells := range rows {
		cell := cellAt(cells, index)
		day, ok := WrittenDay(cell)
		if !ok || day.Slashed == nil {
			continue
		}
		if order := day.Order(); order != "" {
			if _, seen := shown[order]; !seen {
				shown[order] = cell
			}
		} else if day.Slashed[0] != day.Slashed[1] && unsure == "" {
			unsure = cell
		}
	}
	if len(shown) > 1 {
		return "", errs.Inputf("the column '%s' of %s holds both UK and US dates, such as %s and %s",
			column, source, shown[UK], shown[US]).
			WithHint("write them all one way, or as YYYY-MM-DD, or keep them as dates in Excel")
	}
	for order := range shown {
		return order, nil
	}
	if unsure != "" {
		return "", errs.Inputf("cannot tell whether the dates in the column '%s' of %s are UK or US, such as %s: "+
			"none has a number over 12 to say which", column, source, unsure).
			WithHint("write them as YYYY-MM-DD, or keep them as dates in Excel")
	}
	return "", nil
}

func bothWays(day Day) string {
	var readings []string
	for _, order := range []Order{UK, US} {
		if read, ok := day.Read(order); ok {
			text := read.Format("2006-01-02")
			if !slices.Contains(readings, text) {
				readings = append(readings, text)
			}
		}
	}
	return "write it as YYYY-MM-DD: " + strings.Join(readings, " or ")
}
