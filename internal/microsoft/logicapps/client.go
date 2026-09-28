package logicapps

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Consumption Logic Apps in Azure: read deployed workflows, and ask Azure to validate one.
//
// Validating goes to the resource provider's validate endpoint, which type-checks a whole
// definition and gives the provider's own verdict while creating and costing nothing. It
// is the authority the offline checks defer to. It validates the definition, not the
// estate around it, so a missing dispatch target (NestedWorkflowNotFound) is for
// DeployOrder to prevent, not for this to catch.

// APIVersion is the Logic Apps API version read.
const APIVersion = "2019-05-01"

const groupAPI = "2021-04-01"

var (
	workflowName = regexp.MustCompile(`^[A-Za-z0-9._()-]{1,90}$`)
	locationName = regexp.MustCompile(`^[a-z0-9]{2,40}$`)
	groupName    = regexp.MustCompile(`^[-\w._()]{1,90}$`)
)

// Validation is the provider's verdict on a definition.
type Validation struct {
	Workflow string `json:"workflow"`
	Location string `json:"location"`
	Valid    bool   `json:"valid"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Client reads Consumption Logic App workflows through ARM.
type Client struct {
	API *httpx.Client
}

// New is a Logic Apps client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.ARM("")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

var apiVersion = url.Values{"api-version": {APIVersion}}

// Workflows is every Consumption workflow in the resource group, as ARM lists it.
func (c *Client) Workflows(ctx context.Context, subscription, resourceGroup string) ([]fields.Object, error) {
	group, err := groupPath(subscription, resourceGroup)
	if err != nil {
		return nil, err
	}
	return httpx.Collect(c.API.All(ctx, group+"/providers/Microsoft.Logic/workflows", apiVersion, "nextLink"))
}

// Workflow is one workflow, whole (its definition, parameter values and all), with its
// keys in the order ARM gave them.
func (c *Client) Workflow(ctx context.Context, subscription, resourceGroup, name string) (yamltext.Ordered, error) {
	group, err := groupPath(subscription, resourceGroup)
	if err != nil {
		return nil, err
	}
	if !workflowName.MatchString(name) {
		return nil, errs.Inputf("'%s' is not a Logic App workflow name", name)
	}
	body, err := c.API.Text(ctx, group+"/providers/Microsoft.Logic/workflows/"+name, httpx.Call{Params: apiVersion})
	if found := errs.As(err); found != nil && found.Status == http.StatusNotFound {
		return nil, errs.NotFoundf("no workflow '%s' in %s", name, resourceGroup)
	}
	if err != nil {
		return nil, err
	}
	decoded, err := yamltext.Decode([]byte(body), true)
	if err != nil {
		return nil, errs.APIf("%s: workflow %s did not return JSON", c.API.Name(), name)
	}
	resource, ok := decoded.(yamltext.Ordered)
	if !ok {
		return nil, errs.APIf("%s: workflow %s did not return a JSON object", c.API.Name(), name)
	}
	return resource, nil
}

// LocationOf is the resource group's region, where a workflow in it would live.
func (c *Client) LocationOf(ctx context.Context, subscription, resourceGroup string) (string, error) {
	group, err := groupPath(subscription, resourceGroup)
	if err != nil {
		return "", err
	}
	data, err := c.API.Get(ctx, group, url.Values{"api-version": {groupAPI}})
	if err != nil {
		return "", err
	}
	location := fields.Text(data, "location")
	if location == "" {
		return "", errs.APIf("resource group %s has no location", resourceGroup)
	}
	return location, nil
}

// Validate asks the provider whether it would accept document; nothing is deployed.
func (c *Client) Validate(ctx context.Context, document Document, subscription, resourceGroup, location, name string) (Validation, error) {
	workflow := name
	if workflow == "" {
		workflow = document.Name
	}
	if workflow == "" {
		workflow = brand.Command + "-validate-probe"
	}
	if !workflowName.MatchString(workflow) {
		return Validation{}, errs.Inputf("'%s' is not a Logic App workflow name", workflow)
	}
	if !locationName.MatchString(location) {
		return Validation{}, errs.Inputf("'%s' is not an Azure region name, such as uksouth", location)
	}
	group, err := groupPath(subscription, resourceGroup)
	if err != nil {
		return Validation{}, err
	}
	// The body is a workflow resource: the definition, and the parameter VALUES beside it.
	// A declaration with no value is exactly what the provider rejects.
	properties := yamltext.Ordered{{Key: "definition", Value: document.Definition}}
	if document.ParameterValues != nil {
		properties = append(properties, yamltext.Pair{Key: "parameters", Value: document.ParameterValues})
	}
	path := group + "/providers/Microsoft.Logic/locations/" + location + "/workflows/" + workflow + "/validate"
	_, err = c.API.Do(ctx, http.MethodPost, path, httpx.Call{Params: apiVersion, AllowEmpty: true,
		JSON: yamltext.Ordered{{Key: "location", Value: location}, {Key: "properties", Value: properties}}})
	if found := errs.As(err); found != nil && found.Status == http.StatusBadRequest {
		return Validation{Workflow: workflow, Location: location, Valid: false, Code: found.Code, Message: found.Message}, nil
	}
	if err != nil {
		return Validation{}, err
	}
	return Validation{Workflow: workflow, Location: location, Valid: true, Message: "accepted"}, nil
}

func groupPath(subscription, resourceGroup string) (string, error) {
	if !util.IsGUID(subscription) {
		return "", errs.Inputf("'%s' is not a subscription id", subscription)
	}
	if !groupName.MatchString(resourceGroup) || strings.HasSuffix(resourceGroup, ".") {
		return "", errs.Inputf("'%s' is not a resource group name", resourceGroup)
	}
	return "/subscriptions/" + subscription + "/resourceGroups/" + url.PathEscape(resourceGroup), nil
}
