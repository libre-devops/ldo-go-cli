package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/jsontext"
	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
)

// jsonCommand is json: pretty-print JSON from anywhere, in colour on a terminal, or as
// YAML. For JSON that did not come from this tool: az rest, curl, a file. It reads one
// JSON document, or JSON Lines (one document per line, as OTLP logs and many APIs write
// them).
func jsonCommand(rt *Runtime) *cobra.Command {
	var sortKeys, compact, asYAML, painted, plain bool
	var indent int
	command := &cobra.Command{
		Use:   "json [FILE]",
		Short: "Pretty-print JSON from stdin or a file, in colour on a terminal, or as YAML.",
		Long: "Pretty-print JSON from stdin or a file, in colour on a terminal, or as YAML.\n\n" +
			"For JSON from anywhere, e.g. az rest --url ... | " + brand.Command + " json. It reads one document, or JSON Lines, and " +
			"shows it indented, with keys, strings, numbers and brackets coloured (brackets by how deeply they nest). Piped on, " +
			"it stays plain. --yaml writes YAML. Omit FILE, or pass -, to read stdin.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if indent < 0 || indent > 8 {
				return Usagef("--indent", "%d is not in the range 0<=x<=8", indent)
			}
			text, err := jsonInput(rt, args)
			if err != nil {
				return err
			}
			documents, err := jsonDocuments(text)
			if err != nil {
				return err
			}
			colourOn := rt.Console.ColourOut()
			if cmd.Flags().Changed("colour") || cmd.Flags().Changed("no-colour") {
				colourOn = painted && !plain
			}
			if asYAML {
				if compact {
					return errs.Inputf("--compact is for JSON; YAML has no one-line form here")
				}
				return writeYAML(rt, documents, sortKeys, indent, colourOn)
			}
			for _, document := range documents {
				rt.Console.Println(prettyJSON(document, indent, compact, sortKeys, colourOn))
			}
			return nil
		},
	}
	flags := command.Flags()
	flags.BoolVar(&sortKeys, "sort-keys", false, "Sort object keys.")
	flags.BoolVarP(&compact, "compact", "c", false, "One line per document, no spaces.")
	flags.IntVar(&indent, "indent", 2, "Spaces per level.")
	flags.BoolVar(&asYAML, "yaml", false, "Write it as YAML instead.")
	flags.BoolVar(&painted, "colour", false, "Colour it, e.g. for less -R. Default: on a terminal, unless NO_COLOR is set.")
	flags.BoolVar(&plain, "no-colour", false, "Never colour it.")
	return command
}

func prettyJSON(document any, indent int, compact, sortKeys, painted bool) string {
	opts := jsontext.Options{Indent: indent, Lines: !compact, Compact: compact, SortKeys: sortKeys}
	if painted {
		opts.Paint = colour.Paint
		opts.Bracket = func(depth int, text string) string {
			return colour.Style(text, colour.Rainbow[depth%len(colour.Rainbow)], true, false)
		}
	}
	return jsontext.Write(document, opts)
}

func writeYAML(rt *Runtime, documents []any, sortKeys bool, indent int, painted bool) error {
	var paint yamltext.Paint
	if painted {
		paint = colour.Paint
	}
	if indent == 0 {
		indent = 2
	}
	for number, document := range documents {
		if number > 0 {
			separator := "---"
			if painted {
				separator = colour.Style(separator, nil, false, true)
			}
			rt.Console.Println(separator)
		}
		if sortKeys {
			document = sortedValue(document)
		}
		rt.Console.Println(strings.TrimSuffix(yamltext.Dumps(document, indent, paint), "\n"))
	}
	return nil
}

// sortedValue is value with every object's keys sorted.
func sortedValue(value any) any {
	switch v := value.(type) {
	case yamltext.Ordered:
		sorted := make(yamltext.Ordered, len(v))
		copy(sorted, v)
		for index := range sorted {
			sorted[index].Value = sortedValue(sorted[index].Value)
		}
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0 && sorted[j].Key < sorted[j-1].Key; j-- {
				sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
			}
		}
		return sorted
	case []any:
		items := make([]any, len(v))
		for index, item := range v {
			items[index] = sortedValue(item)
		}
		return items
	}
	return value
}

func jsonInput(rt *Runtime, args []string) (string, error) {
	if len(args) == 1 && args[0] != "-" {
		data, err := os.ReadFile(args[0])
		if errors.Is(err, fs.ErrNotExist) {
			return "", errs.Inputf("no such file: %s", args[0])
		}
		if err != nil {
			return "", errs.Inputf("cannot read %s: %v", args[0], err)
		}
		return string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), nil
	}
	if rt.StdinIsTerminal {
		return "", errs.Inputf("no JSON to show").WithHint("pipe some in, e.g. az rest --url ... | %s json", brand.Command)
	}
	return rt.ReadAll()
}

// jsonDocuments is one JSON document, or each line of JSON Lines. A bad one says where
// it broke.
func jsonDocuments(text string) ([]any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, errs.Inputf("the input is empty")
	}
	whole, err := yamltext.Decode([]byte(text), true)
	if err == nil {
		return []any{whole}, nil
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) < 2 {
		return nil, notJSON(text, err)
	}
	var documents []any
	for _, line := range lines {
		document, lineErr := yamltext.Decode([]byte(line), true)
		if lineErr != nil {
			// Neither one document nor JSON Lines: report the document's own error.
			return nil, notJSON(text, err)
		}
		documents = append(documents, document)
	}
	return documents, nil
}

// notJSON is the decoder's complaint, and where in the text it is.
func notJSON(text string, err error) error {
	var syntax *json.SyntaxError
	var extra *yamltext.ExtraData
	switch {
	case errors.As(err, &syntax):
		// Offset counts the byte that broke it; the column is where that byte is.
		return errs.Inputf("not JSON: %s at %s", syntax.Error(), place(text, int(syntax.Offset)-1))
	case errors.As(err, &extra):
		return errs.Inputf("not JSON: %s at %s", extra.Error(), place(text, extra.Offset))
	}
	return errs.Inputf("not JSON: %v", err)
}

// place is "line L, column C" for the byte at index in text, counting from 1.
func place(text string, index int) string {
	before := text[:max(0, min(index, len(text)))]
	line := strings.Count(before, "\n") + 1
	column := len(before) - strings.LastIndex(before, "\n")
	return fmt.Sprintf("line %d, column %d", line, column)
}
