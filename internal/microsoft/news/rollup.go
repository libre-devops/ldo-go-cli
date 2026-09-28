package news

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/sorting"
)

// Message Center rollups: a month's posts summed up in one Planner task.
//
// A month's rollup lists every post last changed in that month, laid out as the tool
// before this one wrote them: the total by severity, each service's and category's count,
// then a line for each post, newest change first. A task's title says which month it
// rolls up. A post a rollup listed before and no longer among the month's (changed again
// since, or gone from Message Center) keeps its line, so bringing a rollup up to date loses
// nothing.

// RollupTitle finds the month a rollup task's title names, as its first group; the tool
// before this one also wrote "month 2026-07".
var RollupTitle = regexp.MustCompile(`(?i)^Message Center rollup: (?:month )?(\d{4}-\d{2})\b`)

// DescriptionLimit is well within what Planner takes (it does not say how much).
const DescriptionLimit = 25000

var listedLine = regexp.MustCompile(`(?im)^- (MC[0-9]+) `)

var severityOrder = []string{"critical", "high", "normal"}

const earlierNote = "Listed before, and no longer among the month's posts: changed again since, or gone from Message Center."

// Rollup is one month's posts (2026-09), as a Planner task's title and description.
// Earlier is the lines for posts listed before, as they were written.
type Rollup struct {
	Month    string
	Messages []Message
	Earlier  []string
}

// Keeping is this rollup, keeping the line description (what it said before) has for each
// post no longer among the month's.
func (r Rollup) Keeping(description string) Rollup {
	ids := r.IDs()
	seen := map[string]bool{}
	var kept []string
	for _, line := range strings.Split(strings.ReplaceAll(description, "\r\n", "\n"), "\n") {
		match := listedLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		id := strings.ToUpper(match[1])
		if !ids[id] && !seen[id] {
			seen[id] = true
			kept = append(kept, strings.TrimSpace(line))
		}
	}
	return Rollup{Month: r.Month, Messages: r.Messages, Earlier: kept}
}

// Title is Message Center rollup: 2026-09 (12 messages).
func (r Rollup) Title() string {
	noun := "messages"
	if len(r.Messages) == 1 {
		noun = "message"
	}
	return fmt.Sprintf("Message Center rollup: %s (%d %s)", r.Month, len(r.Messages), noun)
}

// IDs are the ids of the posts it rolls up.
func (r Rollup) IDs() map[string]bool {
	ids := map[string]bool{}
	for _, message := range r.Messages {
		ids[strings.ToUpper(message.ID)] = true
	}
	return ids
}

// Description is the summary: the counts, then a line for each post, cut to fit Planner.
func (r Rollup) Description() string {
	var services, categories []string
	for _, message := range r.Messages {
		services = append(services, message.Services...)
		categories = append(categories, or(message.Category, "none"))
	}
	head := []string{"# Message Center summary (" + r.Month + ")", "", fmt.Sprintf("Total: %d messages (%s)", len(r.Messages), severities(r.Messages)),
		"", "## By service"}
	head = append(head, counts(services)...)
	head = append(head, "", "## By category")
	head = append(head, counts(categories)...)
	head = append(head, "", "## Messages")
	var lines []string
	for _, message := range r.Messages {
		lines = append(lines, line(message))
	}
	if len(r.Earlier) > 0 {
		lines = append(append(lines, "", "## Listed before", earlierNote), r.Earlier...)
	}
	return fit(strings.Join(head, "\n"), lines)
}

func or(value, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}

// Listed is the ids of the posts a rollup's description lists.
func Listed(description string) map[string]bool {
	ids := map[string]bool{}
	for _, match := range listedLine.FindAllStringSubmatch(description, -1) {
		ids[strings.ToUpper(match[1])] = true
	}
	return ids
}

// Month is one month a span touches: its name (2026-09), its first moment and the next
// month's, in UTC.
type Month struct {
	Name  string
	First time.Time
	After time.Time
}

// Months is each month from the one start is in to the one just before end.
func Months(start, end time.Time) []Month {
	utc := start.UTC()
	first := time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
	var found []Month
	for first.Before(end) && start.Before(end) {
		after := first.AddDate(0, 1, 0)
		found = append(found, Month{Name: first.Format("2006-01"), First: first, After: after})
		first = after
	}
	return found
}

// severities is 0 critical, 1 high, 11 normal, and any other severity Graph gives after
// them, in the order first met.
func severities(messages []Message) string {
	counted := map[string]int{}
	var others []string
	for _, message := range messages {
		name := or(strings.ToLower(message.Severity), "normal")
		if counted[name] == 0 && !contains(severityOrder, name) {
			others = append(others, name)
		}
		counted[name]++
	}
	var parts []string
	for _, name := range append(append([]string(nil), severityOrder...), others...) {
		parts = append(parts, fmt.Sprintf("%d %s", counted[name], name))
	}
	return strings.Join(parts, ", ")
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// counts is a "- name: count" line for each value, the commonest first.
func counts(values []string) []string {
	counted := map[string]int{}
	var names []string
	for _, value := range values {
		if counted[value] == 0 {
			names = append(names, value)
		}
		counted[value]++
	}
	sort.SliceStable(names, func(a, b int) bool {
		if counted[names[a]] != counted[names[b]] {
			return counted[names[a]] > counted[names[b]]
		}
		return sorting.Compare(names[a], names[b]) < 0
	})
	lines := make([]string, len(names))
	for index, name := range names {
		lines[index] = fmt.Sprintf("- %s: %d", name, counted[name])
	}
	return lines
}

// line is "- MC1183010 2026-07-20 [Microsoft Teams] its title", on one line whatever the
// title holds, so no title can pass for another post's line.
func line(message Message) string {
	day := "-"
	if !message.Updated.IsZero() {
		day = message.Updated.UTC().Format("2006-01-02")
	}
	services := ""
	if len(message.Services) > 0 {
		services = " [" + strings.Join(message.Services, ", ") + "]"
	}
	return "- " + message.ID + " " + day + services + " " + strings.Join(strings.Fields(message.Title), " ")
}

// fit is head and as many of lines as fit Planner, then how many did not.
func fit(head string, lines []string) string {
	room := DescriptionLimit - length(head) - 100 // for the line saying what is left out
	var kept []string
	for _, item := range lines {
		room -= length(item) + 1
		if room < 0 {
			break
		}
		kept = append(kept, item)
	}
	if len(kept) < len(lines) {
		left := len(Listed(strings.Join(lines[len(kept):], "\n")))
		kept = append(kept, fmt.Sprintf("- and %d more, too many for one task", left))
	}
	return strings.Join(append([]string{head}, kept...), "\n")
}
