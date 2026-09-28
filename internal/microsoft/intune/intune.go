// Package intune reads Intune managed devices and their compliance through Microsoft
// Graph v1.0. The Azure CLI's Graph token cannot read Intune, so this needs a profile
// whose credential has DeviceManagementManagedDevices.Read.All.
package intune

import (
	"context"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/httpx"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// Requirements are what Intune needs from a Graph token. The Azure CLI's token has neither.
var Requirements = []microsoft.Requirement{
	{Feature: "intune devices", Resource: "graph", AllOf: [][]string{{"DeviceManagementManagedDevices.Read.All",
		"DeviceManagementManagedDevices.ReadWrite.All"}}},
}

const path = "/v1.0/deviceManagement/managedDevices"

// Select are the managed device properties read.
const Select = "id,deviceName,operatingSystem,osVersion,complianceState,managementAgent,lastSyncDateTime," +
	"enrolledDateTime,azureADDeviceId,userPrincipalName,serialNumber,managedDeviceOwnerType"

// ManagedDevice is an Intune managed device. AzureADDeviceID links it to the Entra device.
type ManagedDevice struct {
	ID                string
	DeviceName        string
	OperatingSystem   string
	OSVersion         string
	ComplianceState   string
	ManagementAgent   string
	LastSync          time.Time
	Enrolled          time.Time
	AzureADDeviceID   string
	UserPrincipalName string
	SerialNumber      string
	OwnerType         string
	Raw               fields.Object
}

// Compliant reports whether Intune rates the device compliant.
func (d ManagedDevice) Compliant() bool { return strings.EqualFold(d.ComplianceState, "compliant") }

// DeviceFrom is a managed device as Graph returns it.
func DeviceFrom(data fields.Object) ManagedDevice {
	linked := fields.Text(data, "azureADDeviceId")
	// Intune reports an unlinked device with the all-zero GUID.
	if linked == "00000000-0000-0000-0000-000000000000" {
		linked = ""
	}
	return ManagedDevice{
		ID: fields.Text(data, "id"), DeviceName: fields.Text(data, "deviceName"), OperatingSystem: fields.Text(data, "operatingSystem"),
		OSVersion: fields.Text(data, "osVersion"), ComplianceState: fields.Text(data, "complianceState"),
		ManagementAgent: fields.Text(data, "managementAgent"), LastSync: fields.When(data, "lastSyncDateTime"),
		Enrolled: fields.When(data, "enrolledDateTime"), AzureADDeviceID: linked,
		UserPrincipalName: fields.Text(data, "userPrincipalName"), SerialNumber: fields.Text(data, "serialNumber"),
		OwnerType: fields.Text(data, "managedDeviceOwnerType"), Raw: data,
	}
}

// Client reads Intune for one tenant.
type Client struct {
	API *httpx.Client
}

// New is an Intune client for api's tenant.
func New(api microsoft.API) (*Client, error) {
	client, err := api.Graph("Intune (Microsoft Graph)")
	if err != nil {
		return nil, err
	}
	return &Client{API: client}, nil
}

// FindDevices is the managed devices named name, else its short hostname, newest sync
// first.
func (c *Client) FindDevices(ctx context.Context, name string) ([]ManagedDevice, error) {
	for _, candidate := range util.CandidateNames(name) {
		found, err := c.query(ctx, "deviceName eq "+util.ODataString(candidate))
		if err != nil || len(found) > 0 {
			return found, err
		}
	}
	return nil, nil
}

// ForEntraDevice is the managed devices linked to an Entra deviceId (not its object id).
func (c *Client) ForEntraDevice(ctx context.Context, deviceID string) ([]ManagedDevice, error) {
	id, err := util.RequireGUID(deviceID, "an Entra device id")
	if err != nil {
		return nil, err
	}
	return c.query(ctx, "azureADDeviceId eq "+util.ODataString(id))
}

func (c *Client) query(ctx context.Context, expression string) ([]ManagedDevice, error) {
	items, err := httpx.Collect(c.API.All(ctx, path, url.Values{"$filter": {expression}, "$select": {Select}}, ""))
	if err != nil {
		return nil, err
	}
	devices := make([]ManagedDevice, len(items))
	for index, item := range items {
		devices[index] = DeviceFrom(item)
	}
	sort.SliceStable(devices, func(a, b int) bool { return devices[a].LastSync.After(devices[b].LastSync) })
	return devices, nil
}
