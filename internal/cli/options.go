package cli

import (
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/inputs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/rowfilters"
	"github.com/libre-devops/ldo-go-cli/internal/core/sorting"
	"github.com/libre-devops/ldo-go-cli/internal/core/timewindow"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// Common is the options every data command takes: -o, -p, and for a list, --sort and
// --unique.
type Common struct {
	output  string
	Profile string
	sorts   []string
	unique  []string
	list    bool
}

// AddOutput gives cmd -o and -p. A list command also gets --sort and --unique.
func (c *Common) AddOutput(cmd *cobra.Command, list bool) {
	flags := cmd.Flags()
	flags.StringVarP(&c.output, "output", "o", "table", "table for people, json for scripts, csv for spreadsheets, "+
		"tsv for shell pipelines (no header, as az -o tsv), html for a page to open or share.")
	flags.StringVarP(&c.Profile, "profile", "p", "", "Profile from the config file. Default: $"+brand.ProfileEnv+
		", else default_profile, else the az active account.")
	c.list = list
	if list {
		flags.StringArrayVar(&c.sorts, "sort", nil, "Sort the rows by a column, named as in the table; add :desc to "+
			"reverse it. Repeat it to sort by more, most significant first. Numbers, versions, severities and dates "+
			"sort as such. Table, CSV and TSV.")
		flags.StringArrayVar(&c.unique, "unique", nil, "Keep only the first row for each value of a column, ignoring "+
			"case; repeat it for each combination of several. After --sort, so sorting newest first keeps the newest.")
	}
}

// AddProfile gives cmd -p alone, for a command with no data to shape.
func (c *Common) AddProfile(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&c.Profile, "profile", "p", "", "Profile from the config file. Default: $"+brand.ProfileEnv+
		", else default_profile, else the az active account.")
}

// Output is the -o shape, with --sort and --unique set on the console: a usage error
// for a shape or sort there is not.
func (c *Common) Output(rt *Runtime) (render.Output, error) {
	output, err := render.ParseOutput(c.output)
	if err != nil {
		return "", Usagef("--output", "%q is not one of table, json, csv, tsv, html", c.output)
	}
	rt.Console.Sort, rt.Console.Unique = nil, nil
	for _, spec := range c.sorts {
		name, descending, err := sorting.ParseSort(spec)
		if err != nil {
			return "", Usagef("--sort", "cannot sort by %q; use a column name, with :desc to reverse it", spec)
		}
		rt.Console.Sort = append(rt.Console.Sort, render.Order{Column: name, Descending: descending})
	}
	rt.Console.Unique = append(rt.Console.Unique, c.unique...)
	return output, nil
}

// Duration reads a duration option (15m, 2h, 7d), reporting a bad one as usage.
func Duration(option, value string) (time.Duration, error) {
	span, err := util.ParseDuration(value)
	if err != nil {
		return 0, Usagef(option, "%s", strings.TrimPrefix(err.Error(), "cannot read the duration "))
	}
	return span, nil
}

// NameFlags are how a command that takes a list of names reads them: arguments, - for
// stdin, and -f FILE with --column, --sheet and --where.
type NameFlags struct {
	file   string
	column string
	sheet  string
	where  []string
}

// Add gives cmd -f, --column, --sheet and --where.
func (n *NameFlags) Add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&n.file, "from-file", "f", "", "Read names from a file: one per line, or a column of a CSV or "+
		"Excel workbook (.xlsx, .xlsm, .xltx, .xltm; see --column and --sheet).")
	flags.StringVar(&n.column, "column", "", "Column holding the names, matched by header. Title rows above it are skipped.")
	flags.StringVar(&n.sheet, "sheet", "", "Workbook sheet (tab) to read. Default: the one visible sheet with --column, "+
		"or the first visible sheet.")
	flags.StringArrayVar(&n.where, "where", nil, `Only the rows of the file where COLUMN=VALUE, e.g. "Scheduled Date=tomorrow", `+
		"or COLUMN!=VALUE to leave rows out. Repeatable: values for one column are alternatives, and every column "+
		"must match. A date is today, tomorrow, yesterday, 2026-09-25, or UK or US (25/09/2026, 09/25/2026); days are "+
		"FROM..TO (either end may be left open), last 7d or next 7d.")
}

// Names are the names given, from the rows --where keeps; at least one is needed. what
// says what they are, for the error without one.
func (n *NameFlags) Names(rt *Runtime, args []string, what string) ([]string, error) {
	if n.file != "" {
		if info, err := os.Stat(n.file); err != nil || info.IsDir() {
			return nil, Usagef("--from-file", "file %q does not exist", n.file)
		}
	}
	conditions, err := rowfilters.Parse(n.where, rt.Clock().Local())
	if err != nil {
		return nil, err
	}
	found, err := inputs.ReadNames(args, inputs.Options{Stdin: rt.Stdin, File: n.file, Column: n.column,
		Sheet: n.sheet, Where: conditions})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, errs.Inputf("no %s given", what).WithHint("pass %s, '-' for stdin, or --from-file", what)
	}
	if len(conditions) > 0 {
		var kept []string
		for _, condition := range conditions {
			kept = append(kept, condition.Text)
		}
		rt.Console.Note("%d %s(s) from the rows where %s", len(found), strings.TrimSuffix(what, "s"), strings.Join(kept, " and "))
	}
	return found, nil
}

// TimeFlags are the options that name a window of time: --today, --yesterday, --since,
// or --from and --to.
type TimeFlags struct {
	today     bool
	yesterday bool
	since     string
	from      string
	to        string
}

// Add gives cmd the window options.
func (w *TimeFlags) Add(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.BoolVar(&w.today, "today", false, "Since midnight, local time.")
	flags.BoolVar(&w.yesterday, "yesterday", false, "The whole of yesterday.")
	flags.StringVar(&w.since, "since", "", "The last span of time, e.g. 6h, 7d.")
	flags.StringVar(&w.from, "from", "", "From this day (YYYY-MM-DD, today or yesterday), whole, or this moment "+
		"(YYYY-MM-DDTHH:MM, local time; end it with Z for UTC).")
	flags.StringVar(&w.to, "to", "", "Up to and including this day (YYYY-MM-DD), or up to this moment.")
}

// Window is the window the options name, else fallback's.
func (w *TimeFlags) Window(rt *Runtime, fallback func(time.Time) timewindow.Window) (timewindow.Window, error) {
	var since time.Duration
	if w.since != "" {
		span, err := Duration("--since", w.since)
		if err != nil {
			return timewindow.Window{}, err
		}
		since = span
	}
	return timewindow.Choose(timewindow.Choice{Today: w.today, Yesterday: w.yesterday, Since: since, From: w.from, To: w.to,
		Default: fallback}, rt.Clock().Local())
}
