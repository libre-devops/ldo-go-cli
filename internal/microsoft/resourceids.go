package microsoft

import (
	"regexp"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// What a part of an id may hold: anything but a separator, a URL's special characters
// (query, fragment, escapes) or white space. Names are the service's to restrict further.
var idPart = regexp.MustCompile(`^[^/?#%\\\s]+$`)

const idExample = "/subscriptions/SUBSCRIPTION_ID/resourceGroups/GROUP/providers/NAMESPACE/TYPE/NAME"

// ResourceID is a parsed Azure resource id, read by what each part is, never by counting
// slashes. Types and Names are the resource's own, after its last /providers/NAMESPACE,
// child types included; Parent is the resource an extension resource is on.
//
// Every part is checked as it is read (no empty parts, no . or .., nothing a URL treats
// specially), so an id that parses can go into a request path safely.
type ResourceID struct {
	ID              string
	Subscription    string
	ResourceGroup   string
	ManagementGroup string
	Namespace       string
	Types           []string
	Names           []string
	Parent          *ResourceID
}

// Type is the full resource type (Microsoft.KeyVault/vaults/secrets); for a subscription,
// a resource group or a management group, the type Resource Manager gives those.
func (r ResourceID) Type() string {
	switch {
	case r.Namespace != "":
		return strings.Join(append([]string{r.Namespace}, r.Types...), "/")
	case r.ResourceGroup != "":
		return "Microsoft.Resources/resourceGroups"
	case r.Subscription != "":
		return "Microsoft.Resources/subscriptions"
	}
	return ""
}

// Name is the resource's own name: the last one in the id.
func (r ResourceID) Name() string {
	if len(r.Names) > 0 {
		return r.Names[len(r.Names)-1]
	}
	for _, name := range []string{r.ResourceGroup, r.ManagementGroup, r.Subscription} {
		if name != "" {
			return name
		}
	}
	return ""
}

// Scope is the id of what this sits in or on: the resource an extension is on; for any
// other resource, its resource group, else its subscription; for a resource group, its
// subscription. Empty for a subscription, a management group or a tenant-level resource.
func (r ResourceID) Scope() string {
	if r.Parent != nil {
		return r.Parent.ID
	}
	subscription := "/subscriptions/" + r.Subscription
	switch {
	case r.Namespace == "" && r.ResourceGroup != "":
		return subscription
	case r.Namespace == "":
		return ""
	case r.ResourceGroup != "":
		return subscription + "/resourceGroups/" + r.ResourceGroup
	case r.Subscription != "":
		return subscription
	}
	return ""
}

// IsType reports whether this is a resourceType, whatever the case.
func (r ResourceID) IsType(resourceType string) bool {
	return strings.EqualFold(r.Type(), resourceType)
}

// Parts is the id's parts, by the names Terraform's provider::azurerm::parse_resource_id
// gives them, plus id and management_group_name. Empty parts are nil.
func (r ResourceID) Parts() map[string]any {
	parents := map[string]any{}
	for index := 0; index+1 < len(r.Types); index++ {
		parents[r.Types[index]] = r.Names[index]
	}
	resourceType := r.Type()
	if len(r.Types) > 0 {
		resourceType = r.Types[len(r.Types)-1]
	} else if index := strings.LastIndex(resourceType, "/"); index >= 0 {
		resourceType = resourceType[index+1:]
	}
	var scope any
	if r.Parent != nil {
		scope = r.Parent.ID
	}
	return map[string]any{
		"id": r.ID, "full_resource_type": r.Type(), "parent_resources": parents,
		"resource_group_name": orNil(r.ResourceGroup), "resource_name": r.Name(),
		"resource_provider": orNil(r.Namespace), "resource_scope": scope,
		"resource_type": resourceType, "subscription_id": orNil(r.Subscription),
		"management_group_name": orNil(r.ManagementGroup),
	}
}

func orNil(text string) any {
	if text == "" {
		return nil
	}
	return text
}

// LooksLikeResourceID reports whether text is written as a resource id is (its leading
// slash may be missing), before anything checks that it is one.
func LooksLikeResourceID(text string) bool {
	value := strings.ToLower(strings.Trim(strings.TrimSpace(text), "/"))
	return strings.HasPrefix(value, "subscriptions/") || strings.HasPrefix(value, "providers/")
}

// ParseResourceID is text as a ResourceID, or an Input error saying what is wrong with it.
func ParseResourceID(text string) (ResourceID, error) {
	value := strings.TrimSpace(text)
	if !LooksLikeResourceID(value) {
		return ResourceID{}, errs.Inputf("not an Azure resource id: %q", text).
			WithHint("a resource id starts /subscriptions/ or /providers/, e.g. %s", idExample)
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	for _, part := range parts {
		if !idPart.MatchString(part) || part == "." || part == ".." {
			return ResourceID{}, errs.Inputf("not an Azure resource id: %q (the part %q cannot be in one)", text, part)
		}
	}
	return readID(parts, "/"+strings.Join(parts, "/"), text)
}

// TryParseResourceID is text as a ResourceID, or false when it is not one: for reading ids
// from an API, where an odd one should not fail a whole listing.
func TryParseResourceID(text string) (ResourceID, bool) {
	found, err := ParseResourceID(text)
	return found, err == nil
}

type section struct {
	namespace string
	types     []string
	names     []string
}

func readID(parts []string, whole, given string) (ResourceID, error) {
	subscription, group := "", ""
	index := 0
	if strings.EqualFold(parts[0], "subscriptions") {
		if len(parts) < 2 {
			return ResourceID{}, errs.Inputf("not an Azure resource id: %q (subscriptions has no value)", given)
		}
		if !util.IsGUID(parts[1]) {
			return ResourceID{}, errs.Inputf("not an Azure resource id: %q (%q is not a GUID)", given, parts[1])
		}
		subscription = strings.ToLower(parts[1])
		index = 2
		if len(parts) > index && strings.EqualFold(parts[index], "resourcegroups") {
			if len(parts) < index+2 {
				return ResourceID{}, errs.Inputf("not an Azure resource id: %q (resourceGroups has no value)", given)
			}
			group = parts[index+1]
			index += 2
		}
	}
	sections, err := providerSections(parts[index:], given)
	if err != nil {
		return ResourceID{}, err
	}
	managementGroup := ""
	if len(sections) > 0 && strings.EqualFold(sections[0].namespace, "microsoft.management") &&
		strings.EqualFold(sections[0].types[0], "managementgroups") {
		managementGroup = sections[0].names[0]
	}
	resource := ResourceID{ID: whole, Subscription: subscription, ResourceGroup: group, ManagementGroup: managementGroup}
	var parent *ResourceID
	for _, found := range sections {
		index += 2 + 2*len(found.types)
		resource = ResourceID{
			ID: "/" + strings.Join(parts[:index], "/"), Subscription: subscription, ResourceGroup: group,
			ManagementGroup: managementGroup, Namespace: found.namespace, Types: found.types,
			Names: found.names, Parent: parent,
		}
		current := resource
		parent = &current
	}
	return resource, nil
}

// providerSections reads providers/NAMESPACE/TYPE/NAME[/TYPE/NAME...], once or more
// (extensions).
func providerSections(parts []string, given string) ([]section, error) {
	var sections []section
	for index := 0; index < len(parts); {
		if !strings.EqualFold(parts[index], "providers") || index+1 >= len(parts) {
			at := strings.Join(parts[index:], "/")
			if at == "" {
				at = "the end"
			}
			return nil, errs.Inputf("not an Azure resource id: %q (expected providers/NAMESPACE at %s)", given, at).
				WithHint("e.g. %s", idExample)
		}
		found := section{namespace: parts[index+1]}
		index += 2
		for index < len(parts) && !strings.EqualFold(parts[index], "providers") {
			if index+1 >= len(parts) {
				return nil, errs.Inputf("not an Azure resource id: %q (the type %q has no name)", given, parts[index])
			}
			found.types = append(found.types, parts[index])
			found.names = append(found.names, parts[index+1])
			index += 2
		}
		if len(found.types) == 0 {
			return nil, errs.Inputf("not an Azure resource id: %q (%s names no type)", given, found.namespace)
		}
		sections = append(sections, found)
	}
	return sections, nil
}
