package devices

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/entra"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/intune"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

// One device across Entra, Defender and Intune, and what looks wrong about it.
//
// Entra is required: it is where a device's identity lives. Defender and Intune are
// optional, and a failure reading either one (a suspended service, a token without the
// scope) becomes a finding rather than an error, so the rest of the picture still shows.

// Inspect is everything the services know about name, with findings worth acting on.
// xdrClient and intuneClient may be nil, to leave that service out.
func Inspect(ctx context.Context, name string, entraClient *entra.Client, xdrClient *xdr.Client, intuneClient *intune.Client,
	staleAfter time.Duration, now time.Time) (View, error) {
	view := View{Name: name, Groups: map[string][]entra.Group{}}
	devices, err := entraClient.FindDevices(ctx, name)
	if err != nil {
		return View{}, err
	}
	view.Entra = devices
	for _, device := range devices {
		if view.Groups[device.ID], err = entraClient.DeviceGroups(ctx, device.ID, true); err != nil {
			return View{}, err
		}
	}
	view.Findings = entraFindings(devices)
	if xdrClient != nil {
		lookup, err := xdrClient.FindMachine(ctx, name)
		switch {
		case isAPI(err):
			view.Findings = append(view.Findings, warnf("Defender could not be read: %v", err))
		case err != nil:
			return View{}, err
		default:
			view.Defender = &lookup
			view.Findings = append(view.Findings, defenderFindings(lookup, devices, staleAfter, now)...)
		}
	}
	if intuneClient != nil {
		managed, err := intuneClient.FindDevices(ctx, name)
		switch {
		case isAPI(err):
			view.Findings = append(view.Findings, warnf("Intune could not be read: %v", err))
		case err != nil:
			return View{}, err
		default:
			view.Intune = &managed
			view.Findings = append(view.Findings, intuneFindings(managed, devices)...)
		}
	}
	if !slices.ContainsFunc(view.Findings, func(finding Finding) bool { return finding.Level == "warn" }) {
		view.Findings = append(view.Findings, Finding{"ok", "nothing looks wrong"})
	}
	return view, nil
}

func isAPI(err error) bool {
	found := errs.As(err)
	return found != nil && found.Kind == errs.API
}

func entraFindings(devices []entra.Device) []Finding {
	if len(devices) == 0 {
		return []Finding{warnf("not in Entra")}
	}
	var found []Finding
	if len(devices) > 1 {
		found = append(found, warnf("%d Entra objects share this name (stale registrations)", len(devices)))
	}
	for _, device := range devices {
		if device.Enabled != nil && !*device.Enabled {
			found = append(found, warnf("Entra object %s is disabled", device.ID))
		}
	}
	return found
}

func entraDeviceIDs(devices []entra.Device) []string {
	ids := make([]string, len(devices))
	for index, device := range devices {
		ids[index] = strings.ToLower(device.DeviceID)
	}
	return ids
}

func defenderFindings(lookup xdr.MachineLookup, devices []entra.Device, staleAfter time.Duration, now time.Time) []Finding {
	machine := lookup.Machine()
	if machine == nil {
		return []Finding{warnf("no Defender record")}
	}
	var found []Finding
	if len(lookup.Records) > 1 {
		found = append(found, warnf("%d Defender records; the newest is used", len(lookup.Records)))
	}
	if lookup.MatchedName != strings.TrimRight(strings.TrimSpace(lookup.Query), ".") {
		found = append(found, Finding{"info", "Defender knows it by its short name " + lookup.MatchedName})
	}
	if machine.OnboardingStatus != "Onboarded" {
		found = append(found, warnf("Defender onboarding status is %s", dash(machine.OnboardingStatus)))
	}
	if machine.HealthStatus != "Active" {
		found = append(found, warnf("Defender health is %s", dash(machine.HealthStatus)))
	}
	if !machine.LastSeen.IsZero() && now.Sub(machine.LastSeen) > staleAfter {
		found = append(found, warnf("Defender last saw it %s ago", util.FormatDuration(now.Sub(machine.LastSeen))))
	}
	switch {
	case machine.AADDeviceID == "":
		found = append(found, Finding{"info", "Defender has no Entra device id for it (not Entra joined or registered)"})
	case len(devices) > 0 && !slices.Contains(entraDeviceIDs(devices), strings.ToLower(machine.AADDeviceID)):
		found = append(found, warnf("Defender links it to Entra device %s, which is not one of the Entra objects with this name", machine.AADDeviceID))
	}
	return found
}

func intuneFindings(managed []intune.ManagedDevice, devices []entra.Device) []Finding {
	if len(managed) == 0 {
		return []Finding{{"info", "not enrolled in Intune"}}
	}
	newest := managed[0]
	var found []Finding
	if len(managed) > 1 {
		found = append(found, warnf("%s Intune records; the newest sync is used", strconv.Itoa(len(managed))))
	}
	if !newest.Compliant() {
		found = append(found, warnf("Intune compliance is %s", dash(newest.ComplianceState)))
	}
	if newest.AzureADDeviceID != "" && len(devices) > 0 && !slices.Contains(entraDeviceIDs(devices), strings.ToLower(newest.AzureADDeviceID)) {
		found = append(found, warnf("Intune links it to Entra device %s, which is not one of the Entra objects with this name", newest.AzureADDeviceID))
	}
	return found
}

func dash(text string) string {
	if text == "" {
		return "-"
	}
	return text
}
