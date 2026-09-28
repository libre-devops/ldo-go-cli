package cli

import (
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/browser"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
)

// The page -o html writes is one self-contained, styled HTML file. No font, style or
// script is fetched, so it opens offline, from an email or behind a proxy that blocks
// every CDN. Its content security policy lets the page run only its own style and script
// (by hash) and load nothing at all, and every value in it is escaped.

//go:embed assets/report.css
var reportStyle string

//go:embed assets/report.js
var reportScript string

// The table's colours, as the page's styles name them.
var statusOf = map[string]string{"green": "ok", "yellow": "warn", "red": "err", "bright_black": "muted"}

var hexColour = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// HTMLPage is the whole page for a report, written at generated.
func HTMLPage(report *render.Report, generated time.Time) string {
	accent := brand.Accent
	if !hexColour.MatchString(accent) {
		accent = "#1E3A8A" // put in the CSS as it is, so only a #RRGGBB one is let in
	}
	style := ":root{--accent:" + accent + "}\n" + reportStyle
	policy := fmt.Sprintf("default-src 'none'; style-src '%s'; script-src '%s'; img-src data:; base-uri 'none'; form-action 'none'",
		digest(style), digest(reportScript))
	heading := report.Heading
	if heading == "" {
		heading = brand.Command
	}
	command := report.Command
	if command == "" {
		command = strings.TrimSpace(brand.Command + " " + report.Heading)
	}
	parts := []string{
		"<!doctype html>",
		`<html lang="en"><head><meta charset="utf-8">`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<meta http-equiv="Content-Security-Policy" content="` + policy + `">`,
		"<title>" + e(heading+" | "+brand.DisplayName) + "</title><style>" + style + "</style></head><body>",
		`<header><div class="brand">` + e(brand.DisplayName) + "</div><h1>" + e(heading) + "</h1>",
		"<code>" + e(command) + "</code></header><main>",
		cards(report.Tables),
		notes(report.Notes),
	}
	for _, table := range report.Tables {
		parts = append(parts, panel(table))
	}
	parts = append(parts, "</main>",
		"<footer>Written "+e(generated.Format("2006-01-02 15:04 MST"))+" by "+e(brand.Command)+" "+e(brand.Version)+
			`, <a href="`+e(brand.Repository)+`">`+e(brand.Repository)+"</a></footer>",
		"<script>"+reportScript+"</script></body></html>")
	var kept []string
	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n") + "\n"
}

// RowStatus is the worst colour a row carries: err, warn, ok, or "" for none.
func RowStatus(row []render.Cell) string {
	found := map[string]bool{}
	for _, cell := range row {
		found[statusOf[cell.Colour]] = true
	}
	for _, status := range []string{"err", "warn", "ok"} {
		if found[status] {
			return status
		}
	}
	return ""
}

func cards(tables []render.ReportTable) string {
	counts := map[string]int{}
	total := 0
	for _, table := range tables {
		for _, row := range table.Rows {
			counts[RowStatus(row)]++
			total++
		}
	}
	out := `<div class="cards"><div class="card "><b>` + fmt.Sprint(total) + "</b><span>rows</span></div>"
	for _, card := range [][2]string{{"ok", "ok"}, {"warn", "attention"}, {"err", "errors"}} {
		if counts[card[0]] > 0 {
			out += `<div class="card ` + card[0] + `"><b>` + fmt.Sprint(counts[card[0]]) + "</b><span>" + card[1] + "</span></div>"
		}
	}
	return out + "</div>"
}

func notes(found [][2]string) string {
	if len(found) == 0 {
		return ""
	}
	var lines strings.Builder
	for _, note := range found {
		lines.WriteString(`<p class="` + e(note[0]) + `">` + e(note[1]) + "</p>")
	}
	return `<div class="notes">` + lines.String() + "</div>"
}

func panel(table render.ReportTable) string {
	var head, body strings.Builder
	for _, header := range table.Headers {
		head.WriteString("<th>" + e(header) + "</th>")
	}
	for _, row := range table.Rows {
		status := RowStatus(row)
		kind := ""
		if status == "err" || status == "warn" {
			kind = ` class="` + status + `"`
		}
		body.WriteString("<tr" + kind + ">")
		for _, cell := range row {
			body.WriteString("<td>" + cellHTML(cell) + "</td>")
		}
		body.WriteString("</tr>")
	}
	return `<section class="panel"><div class="toolbar">` +
		`<input type="search" placeholder="Filter rows" aria-label="Filter rows">` +
		`<button type="button">Copy as CSV</button><span class="count"></span></div>` +
		`<div class="scroll"><table><thead><tr>` + head.String() + "</tr></thead><tbody>" + body.String() +
		"</tbody></table></div></section>"
}

func cellHTML(cell render.Cell) string {
	status := statusOf[cell.Colour]
	switch {
	case status == "muted":
		return `<span class="muted">` + e(cell.Text) + "</span>"
	case status != "" && cell.Text != "":
		return `<span class="pill ` + status + `">` + e(cell.Text) + "</span>"
	}
	return e(cell.Text)
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

func e(text string) string { return html.EscapeString(text) }

var fileNameUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// writeHTML writes the page: to stdout when that is a pipe or a file, else to a file
// here, which it opens in the browser.
func writeHTML(rt *Runtime, report *render.Report) error {
	now := rt.Clock()
	page := HTMLPage(report, now)
	if !colour.IsTerminal(rt.Console.OutFile) {
		_, err := fmt.Fprint(rt.Console.Out, page)
		return err
	}
	name := strings.Trim(fileNameUnsafe.ReplaceAllString(strings.ToLower(brand.Command+" "+report.Heading), "-"), "-")
	path, err := filepath.Abs(fmt.Sprintf("%s-%s.html", name, now.UTC().Format("20060102-150405")))
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(page), 0o600); err != nil {
		return err
	}
	rt.Console.Note("wrote %s", path)
	// No browser here (a server, a container): the path is noted, which is enough.
	_ = browser.OpenFile(path)
	return nil
}
