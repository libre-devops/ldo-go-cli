package detections

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
)

// Custom detection rules as YAML files for terraform-msgraph-xdr-custom-detection-rules:
// the brownfield half of detections as code. A rule made by hand in the portal becomes
// one YAML file in the Terraform module's analyst layout, ready to review, commit and
// import. It is the conversion LibreDevOpsHelpers' Export-LdoCustomDetectionRule does:
//
//   - Graph's camelCase becomes the module's snake_case authoring schema.
//   - Each file goes under <category>/<rule-name>.yaml, the category being the rule's
//     first ATT&CK tactic in kebab case (command-and-control), else uncategorised.
//   - The rule's server id is kept, since the module keys rules by it, so terraform
//     import addresses and later plans line up. Leaving it out is for a backup meant to
//     create the rules anew, in this tenant or another.
//   - A rule written the old way (a schedule period, category and mitreTechniques,
//     impactedAssets, responseActions) is converted where it can be, and what cannot be
//     becomes a TODO(export) comment in the file, never dropped quietly.
//
// Two things the module's schema would refuse are noted as well: a rule Defender turned
// off itself (autoDisabled) is written disabled, since the schema allows only enabled and
// disabled, and a rule with no entity mappings, which the schema requires.
//
// What a tenant's rule holds reaches the file only as YAML values, quoted where they need
// to be, or in a comment flattened to one line, so a rule's name or text cannot add keys.

// SchemaURL is the module's schema, which the files name for editors.
const SchemaURL = "https://raw.githubusercontent.com/libre-devops/terraform-msgraph-xdr-custom-detection-rules" +
	"/main/schema/custom-detection.schema.json"

// Tactics are the 14 enterprise ATT&CK tactics, as the module (and Graph) spell them.
var Tactics = []string{"Reconnaissance", "ResourceDevelopment", "InitialAccess", "Execution", "Persistence",
	"PrivilegeEscalation", "DefenseEvasion", "CredentialAccess", "Discovery", "LateralMovement", "Collection",
	"CommandAndControl", "Exfiltration", "Impact"}

// names are the one family of Graph names whose snake case is not the camelCase split:
// oAuth, which the module writes as oauth.
var names = map[string]string{"oAuthApplications": "oauth_applications", "oAuthAppIdColumn": "oauth_app_id_column"}

var (
	camel   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	unsafe  = regexp.MustCompile(`[^A-Za-z0-9]+`)
	notSafe = regexp.MustCompile(`[^A-Za-z0-9]`)
)

// Exported is one rule as a file: where it goes under the export folder, what it holds,
// and what the person should review before committing it.
type Exported struct {
	Rule  Rule
	Path  string
	Text  string
	Notes []string
}

// ExportRules is each rule as a file of its own. A rule whose name makes the same file
// name as one before it gets its id added (rule-name-1234.yaml), so neither is lost.
func ExportRules(rules []fields.Object, keepID bool, now time.Time, exporter string) []Exported {
	var exported []Exported
	taken := map[string]bool{}
	for _, data := range rules {
		found := ExportRule(data, keepID, now, exporter)
		if taken[found.Path] {
			found.Path = strings.TrimSuffix(found.Path, ".yaml") + "-" + notSafe.ReplaceAllString(found.Rule.ID, "") + ".yaml"
		}
		taken[found.Path] = true
		exported = append(exported, found)
	}
	return exported
}

// ExportRule is one rule (as Graph returns it) as the module's YAML, with a header saying
// where it came from and what to review.
func ExportRule(data fields.Object, keepID bool, now time.Time, exporter string) Exported {
	spec, notes := RuleSpec(data, keepID)
	header := []string{
		"# yaml-language-server: $schema=" + SchemaURL,
		"#",
		"# Exported from Microsoft Defender XDR by " + exporter + " on " + now.UTC().Format("2006-01-02 15:04") + "Z.",
	}
	if keepID {
		header = append(header,
			"# The id is the server assigned rule id, kept on purpose: the Terraform module keys",
			"# rules by id, so terraform import addresses and later plans line up. New rules",
			"# authored by hand should omit id. Review this file before committing.")
	}
	for _, note := range notes {
		header = append(header, "# TODO(export): "+strings.Join(strings.Fields(note), " "))
	}
	rule := RuleFrom(data)
	return Exported{Rule: rule, Path: filePath(rule, spec), Text: strings.Join(header, "\n") + "\n" + yamltext.Dumps(spec, 2, nil), Notes: notes}
}

// RuleSpec is the rule in the module's authoring schema, and the notes for what needs
// review.
func RuleSpec(data fields.Object, keepID bool) (yamltext.Ordered, []string) {
	var notes []string
	spec := yamltext.Ordered{}
	if id := fields.Text(data, "id"); keepID && id != "" {
		spec = append(spec, yamltext.Pair{Key: "id", Value: id})
	}
	spec = append(spec, yamltext.Pair{Key: "display_name", Value: fields.Text(data, "displayName")})
	if description := fields.Text(data, "description"); description != "" {
		spec = append(spec, yamltext.Pair{Key: "description", Value: description})
	}
	spec = append(spec,
		yamltext.Pair{Key: "status", Value: exportStatus(data, &notes)},
		yamltext.Pair{Key: "frequency", Value: exportFrequency(data, &notes)},
		yamltext.Pair{Key: "query", Value: fields.Text(fields.Map(data["queryCondition"]), "queryText")},
		yamltext.Pair{Key: "alert", Value: exportAlert(AlertTemplate(data), &notes)})
	action := fields.Map(data["detectionAction"])
	if groups := deviceGroups(action); len(groups) > 0 {
		spec = append(spec, yamltext.Pair{Key: "device_groups", Value: groups})
	}
	if actions := automatedActions(action, &notes); len(actions) > 0 {
		spec = append(spec, yamltext.Pair{Key: "automated_actions", Value: actions})
	}
	return spec, notes
}

func exportStatus(data fields.Object, notes *[]string) string {
	status := RuleStatus(data)
	if status == "" {
		status = "enabled"
	}
	if strings.EqualFold(status, "autodisabled") {
		*notes = append(*notes, "Defender turned this rule off itself (autoDisabled), usually after its query failed again and "+
			"again; it is exported as disabled: fix the query before enabling it.")
		return "disabled"
	}
	return status
}

func exportFrequency(data fields.Object, notes *[]string) string {
	if frequency := RuleFrequency(data); frequency != "" {
		return frequency
	}
	period := fields.Text(fields.Map(data["schedule"]), "period")
	*notes = append(*notes, "legacy schedule period "+util.PythonRepr(period)+" has no mapping; defaulted to PT24H, review.")
	return "PT24H"
}

func exportAlert(template fields.Object, notes *[]string) yamltext.Ordered {
	alert := yamltext.Ordered{}
	for _, key := range []string{"title", "description"} {
		if value := fields.Text(template, key); value != "" {
			alert = append(alert, yamltext.Pair{Key: key, Value: value})
		}
	}
	alert = append(alert, yamltext.Pair{Key: "severity", Value: fields.Text(template, "severity")})
	if actions := fields.Text(template, "recommendedActions"); actions != "" {
		alert = append(alert, yamltext.Pair{Key: "recommended_actions", Value: actions})
	}
	if entries := exportMitre(template, notes); len(entries) > 0 {
		alert = append(alert, yamltext.Pair{Key: "mitre", Value: entries})
	}
	if details := withoutAnnotations(fields.Map(template["customDetails"])); len(details) > 0 {
		alert = append(alert, yamltext.Pair{Key: "custom_details", Value: details})
	}
	if mappings := entityMappings(template, notes); len(mappings) > 0 {
		alert = append(alert, yamltext.Pair{Key: "entity_mappings", Value: mappings})
	}
	return alert
}

// exportMitre is the tactics, each with its techniques (a sub-technique keeps its parent).
func exportMitre(template fields.Object, notes *[]string) []any {
	var entries []any
	for _, tactic := range fields.Objects(template["tactics"]) {
		entry := yamltext.Ordered{{Key: "tactic", Value: fields.Text(tactic, "tactic")}}
		var techniques []any
		for _, technique := range fields.Objects(tactic["techniques"]) {
			techniques = append(techniques, exportTechnique(technique))
		}
		if len(techniques) > 0 {
			entry = append(entry, yamltext.Pair{Key: "techniques", Value: techniques})
		}
		entries = append(entries, entry)
	}
	category := fields.Text(template, "category")
	if len(entries) > 0 || category == "" {
		return entries
	}
	// The legacy shape: one category, which may be a tactic, and a flat list of techniques.
	var legacy []string
	for _, item := range fields.Items(template["mitreTechniques"]) {
		legacy = append(legacy, fields.String(item))
	}
	if slices.Contains(Tactics, category) {
		entry := yamltext.Ordered{{Key: "tactic", Value: category}}
		if len(legacy) > 0 {
			entry = append(entry, yamltext.Pair{Key: "techniques", Value: legacy})
		}
		return []any{entry}
	}
	carried := strings.Join(legacy, ", ")
	if carried == "" {
		carried = "none"
	}
	*notes = append(*notes, "legacy category "+util.PythonRepr(category)+" is not an ATT&CK tactic; techniques not carried: "+carried+".")
	return nil
}

func exportTechnique(technique fields.Object) any {
	var subs []string
	for _, sub := range fields.Items(technique["subTechniques"]) {
		subs = append(subs, fields.String(sub))
	}
	name := fields.Text(technique, "technique")
	if len(subs) == 0 {
		return name
	}
	return yamltext.Ordered{{Key: "technique", Value: name}, {Key: "sub_techniques", Value: subs}}
}

func entityMappings(template fields.Object, notes *[]string) yamltext.Ordered {
	if mappings := groups(fields.Map(template["entityMappings"])); len(mappings) > 0 {
		return mappings
	}
	if assets := fields.Objects(template["impactedAssets"]); len(assets) > 0 {
		var kinds []string
		for _, asset := range assets {
			kinds = append(kinds, fields.Text(asset, "@odata.type"))
		}
		*notes = append(*notes, "legacy impactedAssets not converted (the module uses entity_mappings); review: "+strings.Join(kinds, ", ")+".")
	} else {
		*notes = append(*notes, "the rule maps no entities; the module needs at least one in entity_mappings.")
	}
	return nil
}

func deviceGroups(action fields.Object) []string {
	scope := fields.Map(action["organizationalScope"])
	items := fields.Items(scope["deviceGroups"])
	if len(items) == 0 {
		items = fields.Items(scope["scopeNames"])
	}
	var found []string
	for _, group := range items {
		if text := fields.String(group); text != "" {
			found = append(found, text)
		}
	}
	return found
}

func automatedActions(action fields.Object, notes *[]string) yamltext.Ordered {
	actions := groups(fields.Map(action["automatedActions"]))
	switch {
	case len(actions) > 0:
		*notes = append(*notes, "the rule carries automated_actions: the module call needs allow_automated_actions = true.")
	case len(fields.Items(action["responseActions"])) > 0:
		*notes = append(*notes, "legacy responseActions not converted (the module uses automated_actions); review the original rule.")
	}
	return actions
}

// groups is entity mapping groups, or automated actions: each group snake cased, with its
// items' keys snake cased too, and the empty ones left out.
func groups(source fields.Object) yamltext.Ordered {
	found := yamltext.Ordered{}
	for _, name := range sortedKeys(source) {
		if strings.HasPrefix(name, "@") {
			continue
		}
		var items []any
		for _, entry := range fields.Objects(source[name]) {
			if item := snakeKeys(entry); len(item) > 0 {
				items = append(items, item)
			}
		}
		if len(items) > 0 {
			found = append(found, yamltext.Pair{Key: snake(name), Value: items})
		}
	}
	return found
}

func snakeKeys(item fields.Object) yamltext.Ordered {
	found := yamltext.Ordered{}
	for _, key := range sortedKeys(item) {
		value := item[key]
		if strings.HasPrefix(key, "@") || value == nil || value == "" {
			continue
		}
		found = append(found, yamltext.Pair{Key: snake(key), Value: value})
	}
	return found
}

// withoutAnnotations is source without Graph's @odata annotations.
func withoutAnnotations(source fields.Object) yamltext.Ordered {
	found := yamltext.Ordered{}
	for _, key := range sortedKeys(source) {
		if !strings.HasPrefix(key, "@") {
			found = append(found, yamltext.Pair{Key: key, Value: source[key]})
		}
	}
	return found
}

func sortedKeys(data fields.Object) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func snake(name string) string {
	if known, ok := names[name]; ok {
		return known
	}
	return strings.ToLower(camel.ReplaceAllString(name, "${1}_${2}"))
}

// filePath is <category>/<slug>.yaml: the first tactic in kebab case, and the rule's name,
// each reduced to letters, digits and hyphens, so no name can reach outside the folder.
func filePath(rule Rule, spec yamltext.Ordered) string {
	tactic := ""
	if alert, ok := spec.Get("alert"); ok {
		if entries, ok := alert.(yamltext.Ordered).Get("mitre"); ok {
			first := entries.([]any)[0].(yamltext.Ordered)
			value, _ := first.Get("tactic")
			tactic, _ = value.(string)
		}
	}
	category := slug(camel.ReplaceAllString(tactic, "${1}-${2}"))
	if category == "" {
		category = "uncategorised"
	}
	name := slug(rule.DisplayName)
	if name == "" {
		id := notSafe.ReplaceAllString(rule.ID, "")
		if id == "" {
			id = "unnamed"
		}
		name = "rule-" + id
	}
	return path.Join(category, name+".yaml")
}

func slug(text string) string {
	return strings.ToLower(strings.Trim(unsafe.ReplaceAllString(text, "-"), "-"))
}

// Written is what became of one exported rule on disk: written, kept (a file was there
// already, and nothing forced it) or refused (a link in the way).
type Written struct {
	Exported Exported
	Target   string
	Result   string
}

// WriteRules writes each rule under folder at its path, creating folders as needed.
//
// A file already there is kept unless force. A symbolic link in the way is never written
// through, and nothing is written outside folder (each path is made of letters, digits and
// hyphens anyway, so this is a second guard, not the first).
func WriteRules(exported []Exported, folder string, force bool) ([]Written, error) {
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return nil, err
	}
	var results []Written
	for _, item := range exported {
		target := filepath.Join(folder, filepath.FromSlash(item.Path))
		result, err := writeOne(item, target, root, force)
		if err != nil {
			return nil, err
		}
		results = append(results, Written{Exported: item, Target: target, Result: result})
	}
	return results, nil
}

func writeOne(item Exported, target, root string, force bool) (string, error) {
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&fs.ModeSymlink != 0 {
			return "refused", nil
		}
		if !force {
			return "kept", nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return "", err
	}
	if relative, err := filepath.Rel(root, parent); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "refused", nil // a linked folder, made or met on the way
	}
	return "written", os.WriteFile(target, []byte(item.Text), 0o644)
}
