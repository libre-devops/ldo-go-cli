package cli

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
)

// newGroup is a command group: help with no command, a usage error for an unknown one.
func newGroup(use, short string) *cobra.Command {
	return &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
}

// title is text in bold, for a heading, when stdout is coloured.
func title(rt *Runtime, text string) string {
	if !rt.Console.ColourOut() {
		return text
	}
	return colour.Style(text, nil, true, false)
}

// pairs is aligned label and value lines, the labels bold when stdout is coloured.
func pairs(rt *Runtime, items [][2]string) string {
	return render.Pairs(items, rt.Console.ColourOut())
}

// readQuery is a query from the argument, from --file, or from stdin, in that order.
func readQuery(rt *Runtime, query, file string) (string, error) {
	if query != "" && query != "-" {
		return query, nil
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if errors.Is(err, fs.ErrNotExist) {
			return "", Usagef("--file", "file %q does not exist", file)
		}
		if err != nil {
			return "", errs.Inputf("cannot read %s: %v", file, err)
		}
		return string(data), nil
	}
	if query == "-" || !rt.StdinIsTerminal {
		text, err := rt.ReadAll()
		if err != nil {
			return "", errs.Inputf("cannot read stdin: %v", err)
		}
		if strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	return "", errs.Inputf("no query given").WithHint("pass it as an argument, with --file, or on stdin")
}

// positive checks an option that must be at least 1, when it was given.
func positive(cmd *cobra.Command, name string, value int) error {
	if cmd.Flags().Changed(name) && value < 1 {
		return Usagef("--"+name, "%d is not in the range x>=1", value)
	}
	return nil
}

// optionalDuration is a duration option, or 0 when it was not given.
func optionalDuration(option, value string) (span int64, err error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := Duration(option, value)
	return int64(parsed), err
}

// orNil is text, or null in JSON when it is empty.
func orNil(text string) any {
	if text == "" {
		return nil
	}
	return text
}

// orNilInt is value, or null in JSON when it is 0 (none).
func orNilInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
