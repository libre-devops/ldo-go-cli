// Package logicapps checks Consumption Logic App workflows offline, asks Azure's own
// validation about them, and exports them. Ported from the LibreDevOpsHelpers LogicApps
// module: the offline half checks a definition against the contract Azure enforces, with
// no network; the online half reads deployed workflows and asks the resource provider to
// validate a definition without deploying it.
//
// Definitions are read keeping their keys in order (yamltext.Ordered), so a file written
// back out (defaults, export) differs from what came in only where it was changed.
package logicapps

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements: ARM access rests on Azure RBAC (Logic App Reader, or Reader), not token
// scopes.
var Requirements []microsoft.Requirement

// A definition arrives in one of three shapes, and the difference matters:
//
//   - code-view: the designer's code view, {"definition": {...}, "parameters": {...}}.
//   - arm: an ARM resource GET, {"properties": {"definition": {...}, ...}, ...}.
//   - bare: a template's bare definition, {"$schema": ..., "triggers": ..., "actions": ...}.
//
// A wrapper's parameters block holds VALUES; a bare definition's top-level parameters holds
// its DECLARATIONS. Reading one as the other is the classic round-trip trap, so the two are
// always kept apart here. A workflow definition has no top-level definition or properties
// key of its own, so telling the shapes apart is exact.
//
// Templates for Terraform's templatefile (.json.tftpl) are not JSON while they hold ${...}
// tokens; those are blanked first, so their shape can still be checked.

// TokenMark is what an unrendered ${...} becomes, so the text parses.
const TokenMark = "TFTPL_TOKEN"

// Shapes are the shapes a document arrives in.
var Shapes = []string{"code-view", "arm", "bare"}

// Document is a workflow document, unwrapped: its definition, and the values beside it.
// ParameterValues is nil for a bare definition, which has none.
type Document struct {
	Source          string
	Name            string
	Shape           string
	Definition      yamltext.Ordered
	ParameterValues yamltext.Ordered
	Text            string
}

// Declarations are the parameters the definition declares, or nil when it declares none.
func (d Document) Declarations() yamltext.Ordered { return object(get(d.Definition, "parameters")) }

// Triggers are the definition's triggers by name.
func (d Document) Triggers() yamltext.Ordered { return object(get(d.Definition, "triggers")) }

// Actions are the definition's actions by name.
func (d Document) Actions() yamltext.Ordered { return object(get(d.Definition, "actions")) }

// object is value as an ordered object, or nil when it is not one.
func object(value any) yamltext.Ordered {
	found, _ := value.(yamltext.Ordered)
	return found
}

// get is an object's value at key, or nil.
func get(value any, key string) any {
	found, _ := object(value).Get(key)
	return found
}

// text is an object's string at key, or "".
func text(value any, key string) string {
	found, _ := get(value, key).(string)
	return found
}

func has(value any, key string) bool {
	_, found := object(value).Get(key)
	return found
}

// TokenSafeJSON is text with each Terraform ${...} token blanked, so it parses as JSON.
//
// Where a token sits decides the replacement, which is why this scans rather than using a
// regex: inside a JSON string it becomes bare text, but standing alone in value position
// ("interval": ${x}) it needs quotes of its own. Escapes and brace nesting are tracked, so a
// token holding braces is one unit. $${ is templatefile's escaped literal (it renders as
// ${), so it is not a token. Logic App expressions use @{...}, so a ${...} is always a
// Terraform token here.
func TokenSafeJSON(source string) string {
	var out strings.Builder
	inString := false
	for index := 0; index < len(source); {
		char := source[index]
		if inString && char == '\\' && index+1 < len(source) {
			out.WriteString(source[index : index+2]) // an escape carries its next character
			index += 2
			continue
		}
		if char == '"' {
			inString = !inString
			out.WriteByte(char)
			index++
			continue
		}
		if strings.HasPrefix(source[index:], "$${") {
			out.WriteString("${")
			index += 3
			continue
		}
		if strings.HasPrefix(source[index:], "${") {
			if end := tokenEnd(source, index+1); end >= 0 {
				if inString {
					out.WriteString(TokenMark)
				} else {
					out.WriteString(`"` + TokenMark + `"`)
				}
				index = end + 1
				continue
			}
		}
		out.WriteByte(char)
		index++
	}
	return out.String()
}

// tokenEnd is the index of the brace that closes the one at opening, or -1.
func tokenEnd(source string, opening int) int {
	depth := 0
	for position := opening; position < len(source); position++ {
		switch source[position] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return position
			}
		}
	}
	return -1
}

// Load reads and unwraps the document at path (.json or .json.tftpl).
func Load(path string) (Document, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Document{}, errs.Inputf("Logic App definition file not found: %s", path)
	}
	if err != nil {
		return Document{}, errs.Inputf("cannot read %s: %v", path, err)
	}
	return Parse(string(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))), path, WorkflowNameFrom(path))
}

// Parse unwraps a document already read, from source (a path, or <string>).
func Parse(source, from, defaultName string) (Document, error) {
	if strings.TrimSpace(source) == "" {
		return Document{}, errs.Inputf("Logic App definition %s is empty", from)
	}
	parsed, err := yamltext.Decode([]byte(TokenSafeJSON(source)), true)
	if err != nil {
		return Document{}, errs.Inputf("Logic App definition %s is not JSON: %v", from, err)
	}
	root, ok := parsed.(yamltext.Ordered)
	if !ok {
		return Document{}, errs.Inputf("Logic App definition %s is not a JSON object", from)
	}
	document := Document{Source: from, Name: defaultName, Text: source}
	if name, ok := get(root, "name").(string); ok {
		document.Name = name
	}
	properties := object(get(root, "properties"))
	switch {
	case object(get(properties, "definition")) != nil:
		document.Shape, document.Definition, document.ParameterValues = "arm", object(get(properties, "definition")), object(get(properties, "parameters"))
	case object(get(root, "definition")) != nil:
		document.Shape, document.Definition, document.ParameterValues = "code-view", object(get(root, "definition")), object(get(root, "parameters"))
	default:
		document.Shape, document.Definition = "bare", root
	}
	return document, nil
}

// WorkflowNameFrom is router for router.json or router.json.tftpl.
func WorkflowNameFrom(path string) string {
	name := filepath.Base(path)
	for _, suffix := range []string{".tftpl", ".json"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

// ActionNode is one action, wherever it sits: Path names it through its containers.
type ActionNode struct {
	Name   string
	Path   string
	Action yamltext.Ordered
}

// ActionNodes is every action, however deeply nested, each with its full path.
//
// The workflow language nests actions: a Scope, Foreach and Until carry actions; an If
// adds else.actions; a Switch carries cases.<name>.actions and default.actions. Reading
// only the top level sees a fraction of the workflow.
func ActionNodes(actions yamltext.Ordered, prefix string) []ActionNode {
	var nodes []ActionNode
	for _, pair := range actions {
		action := object(pair.Value)
		if action == nil {
			continue
		}
		path := pair.Key
		if prefix != "" {
			path = prefix + "/" + pair.Key
		}
		nodes = append(nodes, ActionNode{Name: pair.Key, Path: path, Action: action})
		containers := []yamltext.Ordered{object(get(action, "actions"))}
		for _, branch := range []string{"else", "default"} {
			containers = append(containers, object(get(get(action, branch), "actions")))
		}
		for _, cases := range object(get(action, "cases")) {
			containers = append(containers, object(get(cases.Value, "actions")))
		}
		for _, container := range containers {
			nodes = append(nodes, ActionNodes(container, path)...)
		}
	}
	return nodes
}
