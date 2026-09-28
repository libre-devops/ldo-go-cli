// Package markdown turns HTML into Markdown: a page or a description, readable in a
// terminal and in a file.
//
// It covers what documents are made of: headings, paragraphs, emphasis, links, lists
// (nested), tables, quotes and code, and Confluence's storage format beside them (its code
// macro, its tasks, its links to pages), whose settings are left out. Anything else keeps
// its text. Nothing is fetched.
package markdown

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var (
	blocks   = map[string]bool{"p": true, "div": true, "section": true, "article": true, "header": true, "footer": true, "ac:layout-cell": true}
	emphasis = map[string]string{"strong": "**", "b": "**", "em": "_", "i": "_", "code": "`", "s": "~~", "del": "~~"}
	// Confluence's storage format: tags whose content is a setting, not text.
	hidden = map[string]bool{"ac:parameter": true, "script": true, "style": true, "ri:attachment": true, "ac:emoticon": true}

	spaces    = regexp.MustCompile(`\s+`)
	innerRuns = regexp.MustCompile(`(\S) {2,}(\S)`)
	manyLines = regexp.MustCompile(`\n{3,}`)
)

func heading(tag string) int {
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		return int(tag[1] - '0')
	}
	return 0
}

type list struct {
	ordered bool
	number  int
}

type converter struct {
	// Text is written into the innermost buffer: a link's or a table cell's, or the page.
	buffers   []*strings.Builder
	lists     []*list
	links     []string
	rows      [][]string
	hidden    int
	pre       int
	codeMacro bool
}

// FromHTML is source as Markdown text, ending in a line break.
func FromHTML(source string) string {
	c := &converter{buffers: []*strings.Builder{{}}}
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	for {
		kind := tokenizer.Next()
		if kind == html.ErrorToken {
			break
		}
		token := tokenizer.Token()
		c.handle(kind, token)
	}
	return c.markdown()
}

func (c *converter) handle(kind html.TokenType, token html.Token) {
	attrs := map[string]string{}
	for _, attr := range token.Attr {
		name := attr.Key
		if attr.Namespace != "" {
			name = attr.Namespace + ":" + attr.Key
		}
		attrs[strings.ToLower(name)] = attr.Val
	}
	tag := strings.ToLower(token.Data)
	switch kind {
	case html.StartTagToken:
		c.start(tag, attrs)
	case html.EndTagToken:
		c.end(tag)
	case html.SelfClosingTagToken:
		c.startEnd(tag, attrs)
	case html.TextToken:
		if c.hidden == 0 {
			if c.pre > 0 {
				c.write(token.Data)
			} else {
				c.write(spaces.ReplaceAllString(token.Data, " "))
			}
		}
	case html.CommentToken:
		// CDATA: Confluence keeps a code block's text in one.
		if strings.HasPrefix(token.Data, "[CDATA[") && c.hidden == 0 {
			c.write(strings.TrimSuffix(strings.TrimPrefix(token.Data, "[CDATA["), "]]"))
		}
	}
}

func (c *converter) start(tag string, attrs map[string]string) {
	switch {
	case hidden[tag]:
		c.hidden++
	case heading(tag) > 0:
		c.block(strings.Repeat("#", heading(tag)) + " ")
	case emphasis[tag] != "":
		c.write(emphasis[tag])
	default:
		c.startStructure(tag, attrs)
	}
}

func (c *converter) end(tag string) {
	switch {
	case hidden[tag]:
		c.hidden = max(0, c.hidden-1)
	case heading(tag) > 0 || blocks[tag] || tag == "blockquote":
		c.block("")
	case emphasis[tag] != "":
		c.write(emphasis[tag])
	default:
		c.endStructure(tag)
	}
}

func (c *converter) startEnd(tag string, attrs map[string]string) {
	switch tag {
	case "br":
		if len(c.rows) == 0 {
			c.write("  \n")
		} else {
			c.write(" ")
		}
	case "hr":
		c.block("---")
		c.block("")
	case "img":
		if alt := attrs["alt"]; alt != "" {
			c.write("[" + alt + "]")
		}
	default:
		c.start(tag, attrs)
	}
}

func (c *converter) codeBody(tag string) bool {
	return tag == "pre" || (tag == "ac:plain-text-body" && c.codeMacro)
}

func (c *converter) startStructure(tag string, attrs map[string]string) {
	switch {
	case blocks[tag]:
		c.block("")
	case tag == "ul" || tag == "ol" || tag == "ac:task-list":
		c.lists = append(c.lists, &list{ordered: tag == "ol"})
	case tag == "li" || tag == "ac:task":
		marker := ""
		if tag == "ac:task" {
			marker = "[ ] "
		}
		c.item(marker)
	case tag == "blockquote":
		c.block("> ")
	case c.codeBody(tag):
		c.pre++
		c.block("```\n")
	case tag == "ac:structured-macro":
		c.codeMacro = attrs["ac:name"] == "code" || attrs["ac:name"] == "noformat"
	case tag == "a":
		c.links = append(c.links, attrs["href"])
		c.buffers = append(c.buffers, &strings.Builder{})
	case tag == "tr":
		c.rows = append(c.rows, nil)
	case tag == "td" || tag == "th":
		c.buffers = append(c.buffers, &strings.Builder{})
	}
}

func (c *converter) endStructure(tag string) {
	switch {
	case (tag == "ul" || tag == "ol" || tag == "ac:task-list") && len(c.lists) > 0:
		c.lists = c.lists[:len(c.lists)-1]
		if len(c.lists) == 0 {
			c.block("")
		}
	case c.codeBody(tag):
		c.pre = max(0, c.pre-1)
		c.write("\n```")
		c.block("")
	case tag == "ac:structured-macro":
		c.codeMacro = false
	case tag == "a" && len(c.links) > 0:
		text := strings.TrimSpace(c.pop())
		href := c.links[len(c.links)-1]
		c.links = c.links[:len(c.links)-1]
		switch {
		case href != "" && text != "" && href != text:
			c.write("[" + text + "](" + href + ")")
		case text != "":
			c.write(text)
		default:
			c.write(href)
		}
	case (tag == "td" || tag == "th") && len(c.buffers) > 1 && len(c.rows) > 0:
		cell := strings.ReplaceAll(strings.TrimSpace(c.pop()), "|", `\|`)
		c.rows[len(c.rows)-1] = append(c.rows[len(c.rows)-1], cell)
	case tag == "table" && len(c.rows) > 0:
		c.block(table(c.rows))
		c.block("")
		c.rows = nil
	}
}

func (c *converter) pop() string {
	last := c.buffers[len(c.buffers)-1]
	c.buffers = c.buffers[:len(c.buffers)-1]
	return last.String()
}

func (c *converter) write(text string) { c.buffers[len(c.buffers)-1].WriteString(text) }

func (c *converter) block(start string) {
	// A table cell is one line, and a list item's paragraphs run on after its bullet.
	inCell := len(c.rows) > 0 && len(c.buffers) > 1
	if inCell || (len(c.lists) > 0 && !strings.HasPrefix(start, "```")) {
		c.write(" ")
		return
	}
	c.write("\n\n" + start)
}

func (c *converter) item(marker string) {
	if len(c.lists) == 0 {
		c.lists = append(c.lists, &list{})
	}
	current := c.lists[len(c.lists)-1]
	current.number++
	bullet := "-"
	if current.ordered {
		bullet = strconv.Itoa(current.number) + "."
	}
	if len(c.rows) > 0 && len(c.buffers) > 1 { // in a table cell, the list runs on
		c.write(" " + bullet + " " + marker)
		return
	}
	c.write("\n" + strings.Repeat("  ", len(c.lists)-1) + bullet + " " + marker)
}

func (c *converter) markdown() string {
	for len(c.buffers) > 1 { // an unclosed link or cell keeps its text
		c.write(c.pop())
	}
	var lines []string
	fenced := false
	for _, line := range strings.Split(c.buffers[0].String(), "\n") {
		if strings.HasPrefix(line, "```") {
			fenced = !fenced
		} else if !fenced {
			// One space between words, keeping a hard break's two at the end.
			hard := strings.HasSuffix(line, "  ") && strings.TrimSpace(line) != ""
			line = collapse(strings.TrimRight(line, " \t\r\n\f\v"))
			if hard {
				line += "  "
			}
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(manyLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")) + "\n"
}

// collapse is line with each run of spaces between words made one, as Python's
// (?<=\S) {2,}(?=\S) does; the look-arounds are why this loops.
func collapse(line string) string {
	for {
		next := innerRuns.ReplaceAllString(line, "$1 $2")
		if next == line {
			return line
		}
		line = next
	}
}

func table(rows [][]string) string {
	width := 0
	var padded [][]string
	for _, row := range rows {
		width = max(width, len(row))
	}
	for _, row := range rows {
		if len(row) == 0 {
			continue
		}
		for len(row) < width {
			row = append(row, "")
		}
		padded = append(padded, row)
	}
	if len(padded) == 0 {
		return ""
	}
	lines := []string{"| " + strings.Join(padded[0], " | ") + " |", "|" + strings.Repeat(" --- |", width)}
	for _, row := range padded[1:] {
		lines = append(lines, "| "+strings.Join(row, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}
