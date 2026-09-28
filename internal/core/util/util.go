// Package util holds small helpers shared across the packages: names, OData literals,
// timestamps and durations.
package util

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

var (
	guidPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	// What a host name can hold (letters, digits, dots, hyphens and underscores), and no
	// more, so a name checked by RequireHost is safe inside a query's string literal.
	hostPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
	separators  = regexp.MustCompile(`[,\s]+`)
	// Some Microsoft APIs return seven fractional digits; the layouts below read nine.
	durationPart = regexp.MustCompile(`(?i)(\d+)\s*([dhms])`)
)

// IsGUID reports whether value is a GUID in the 8-4-4-4-12 form.
func IsGUID(value string) bool {
	return guidPattern.MatchString(strings.TrimSpace(value))
}

// RequireGUID is value as a lowercase GUID, else an Input error saying "not <what>".
//
// An id that goes into a URL path or a filter comes through here first, so a path is
// never built from anything else. what names it, with its article: "an object id".
func RequireGUID(value, what string) (string, error) {
	if !IsGUID(value) {
		return "", errs.Inputf("not %s: %q", what, value)
	}
	return strings.ToLower(strings.TrimSpace(value)), nil
}

// ShortName is the host part of a name: web01.corp.example.com is web01.
func ShortName(name string) string {
	name = strings.TrimRight(strings.TrimSpace(name), ".")
	short, _, _ := strings.Cut(name, ".")
	return short
}

// RequireHost is value as a device name that can go into a query, else an Input error.
//
// Only the characters a host name holds are let in, so no quote, space or operator can
// reach the query around it. A trailing dot (an absolute FQDN) is dropped.
func RequireHost(value string) (string, error) {
	name := strings.TrimRight(strings.TrimSpace(value), ".")
	if !hostPattern.MatchString(name) {
		return "", errs.Inputf("%q is not a device name that can go in a query", value).
			WithHint("use the host name or FQDN, e.g. web01 or web01.corp.example")
	}
	return name, nil
}

// CandidateNames are the names to try for one device, most specific first: the FQDN,
// then the short host name. Entra and Defender do not record Linux host names
// consistently, so a lookup that misses on the FQDN retries on the short name.
func CandidateNames(name string) []string {
	full := strings.TrimRight(strings.TrimSpace(name), ".")
	short := ShortName(full)
	if strings.EqualFold(short, full) {
		return []string{full}
	}
	return []string{full, short}
}

// SplitNames splits comma- or whitespace-separated names, dropping blanks and repeats:
// ["a,b", "c  d", "A"] is [a b c d]. A command can take "X,Y,Z" as one argument, as
// several, or as lines from a file. Repeats are matched ignoring case, the first
// spelling kept.
func SplitNames(values []string) []string {
	seen := map[string]bool{}
	var names []string
	for _, value := range values {
		for _, name := range separators.Split(value, -1) {
			folded := strings.ToLower(name)
			if name != "" && !seen[folded] {
				seen[folded] = true
				names = append(names, name)
			}
		}
	}
	return names
}

// ODataString quotes value as an OData string literal, doubling embedded single quotes.
func ODataString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// ODataDatetime is a UTC OData datetime literal, 2026-09-24T10:11:12Z, never quoted.
func ODataDatetime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05Z")
}

var datetimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05.999999999-0700",
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02",
}

// ParseDatetime reads an ISO 8601 timestamp from an API as a UTC time.
//
// It is the zero time for empty or unreadable values rather than an error, because one
// odd record should not fail a whole listing, and for Microsoft's "not known" dates
// (0001-01-01, and 1601-01-01 from Windows).
func ParseDatetime(value any) time.Time {
	text, ok := value.(string)
	if !ok {
		return time.Time{}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}
	}
	for _, layout := range datetimeLayouts {
		parsed, err := time.Parse(layout, text)
		if err != nil {
			continue
		}
		if parsed.Year() < 1900 {
			return time.Time{}
		}
		return parsed.UTC()
	}
	return time.Time{}
}

var unitSeconds = map[string]int{"d": 86400, "h": 3600, "m": 60, "s": 1}

// ParseDuration reads 90, 90s, 15m, 2h, 1h30m or 7d as a duration. A bare number is
// seconds. Anything else, zero included, is an Input error.
func ParseDuration(text string) (time.Duration, error) {
	value := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(text), " ", ""))
	seconds := 0
	if number, err := strconv.Atoi(value); err == nil && value != "" {
		seconds = number
	} else {
		parts := durationPart.FindAllStringSubmatch(value, -1)
		joined := ""
		for _, part := range parts {
			joined += part[1] + part[2]
			number, _ := strconv.Atoi(part[1])
			seconds += number * unitSeconds[part[2]]
		}
		if len(parts) == 0 || joined != value {
			return 0, errs.Inputf("cannot read the duration %q", text).
				WithHint("use e.g. 90, 90s, 15m, 2h, 1h30m or 7d")
		}
	}
	if seconds <= 0 {
		return 0, errs.Inputf("the duration %q must be more than zero", text)
	}
	return time.Duration(seconds) * time.Second, nil
}

// FormatSpan is a span as a person would give it: 7d, 12h or 30m when it is a whole
// number of them, else as FormatDuration writes it.
func FormatSpan(span time.Duration) string {
	seconds := int(span.Seconds())
	for _, unit := range []struct {
		name string
		size int
	}{{"d", 86400}, {"h", 3600}, {"m", 60}} {
		if seconds != 0 && seconds%unit.size == 0 {
			return fmt.Sprintf("%d%s", seconds/unit.size, unit.name)
		}
	}
	return FormatDuration(span)
}

// FormatDuration renders a duration compactly: 45s, 12m 05s, 3h 07m, 2d 04h.
func FormatDuration(span time.Duration) string {
	seconds := int(span.Seconds())
	if seconds < 0 {
		seconds = -seconds
	}
	days, seconds := seconds/86400, seconds%86400
	hours, seconds := seconds/3600, seconds%3600
	minutes, seconds := seconds/60, seconds%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %02dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %02dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// Grouped is value to places decimal places with its thousands grouped by commas, as
// Python's "{:,.2f}" writes it: 1234.5 is 1,234.50.
func Grouped(value float64, places int) string {
	text := strconv.FormatFloat(value, 'f', places, 64)
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	whole, fraction, hasFraction := strings.Cut(text, ".")
	var grouped []string
	for len(whole) > 3 {
		grouped = append([]string{whole[len(whole)-3:]}, grouped...)
		whole = whole[:len(whole)-3]
	}
	grouped = append([]string{whole}, grouped...)
	text = sign + strings.Join(grouped, ",")
	if hasFraction {
		text += "." + fraction
	}
	return text
}
