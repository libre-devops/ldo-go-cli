package news

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/libre-devops/ldo-go-cli/internal/core/markdown"
)

// How a Planner task raised for a Message Center post reads: its title and its notes.
//
// Two layouts. sync, the default, is the one Microsoft's own Message Center sync to
// Planner writes, so a plan can hold both its tasks and these and they look alike:
// [Microsoft Teams] <title> [MC1183010], the notes starting with the post's id, published
// date, category and tags. short is MC1183010: <title>, as the tool before this one wrote
// them. Both notes go on with the post's link and its text.

// TextLimit is how much of the post's text a task's notes hold.
const TextLimit = 6000

// titleRoom is the room a title keeps for the post's own title before its services are
// left out.
const titleRoom = 40

// Layouts are how a task for a post reads.
var Layouts = []string{"sync", "short"}

// TaskTitle is the task's title, no longer than limit: the post's title is what is cut,
// so the id, which says the task is the post's, is always there.
func TaskTitle(message Message, layout string, limit int) string {
	head, tail := message.ID+": ", ""
	if layout != "short" {
		head = ""
		if len(message.Services) > 0 {
			head = "[" + strings.Join(message.Services, ", ") + "] "
		}
		tail = " [" + message.ID + "]"
		if limit-length(head)-length(tail) < titleRoom {
			head = "" // so many services that the title would have no room
		}
	}
	title := strings.Join(strings.Fields(message.Title), " ")
	room := limit - length(head) - length(tail)
	if length(title) > room {
		kept := prefix(title, max(room-3, 0))
		if index := strings.LastIndex(kept, " "); index >= 0 {
			kept = kept[:index] // at a word's end, not in the middle of one
		}
		title = strings.TrimRight(kept, " ") + "..."
	}
	return head + title + tail
}

// TaskNotes is the task's notes: in the sync layout, first the post's id, published date,
// category and tags, as the sync lists them; then its link in the admin centre, and its
// text as Markdown, cut to TextLimit.
func TaskNotes(message Message, layout string) string {
	var lines []string
	if layout != "short" {
		lines = append(details(message), "")
	}
	lines = append(lines, message.URL(), "", text(message))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func details(message Message) []string {
	found := []string{"Message ID: " + message.ID}
	if !message.Starts.IsZero() {
		day := message.Starts.UTC()
		found = append(found, fmt.Sprintf("Published date: %d/%d/%d", day.Month(), day.Day(), day.Year()))
	}
	if message.Category != "" {
		label := message.CategoryLabel()
		found = append(found, "Category: "+strings.ToUpper(label[:1])+label[1:])
	}
	if len(message.Tags) > 0 {
		found = append(found, "Tags: "+strings.Join(message.Tags, ", "))
	}
	return found
}

func text(message Message) string {
	body := ""
	if message.BodyHTML != "" {
		body = strings.TrimSpace(markdown.FromHTML(message.BodyHTML))
	}
	if length(body) > TextLimit {
		body = strings.TrimRight(prefix(body, TextLimit), " \t\n\r\f\v") + "\n\n(more in the admin centre)"
	}
	return body
}

// length is text's length in characters, as Python counts it.
func length(text string) int { return utf8.RuneCountInString(text) }

// prefix is text's first count characters.
func prefix(text string, count int) string {
	runes := []rune(text)
	if count >= len(runes) {
		return text
	}
	return string(runes[:count])
}
