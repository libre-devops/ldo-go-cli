package cli

import (
	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

// xdrCommand is the xdr group: machines, findings, hunting, then incidents.
func xdrCommand(rt *Runtime) *cobra.Command {
	group := newGroup("xdr", "Defender XDR: incidents (Sentinel's included), and Defender for Endpoint machines, "+
		"alerts, vulnerabilities and hunting.")
	group.AddCommand(xdrMachines(rt), xdrStale(rt), xdrAlerts(rt), xdrVulns(rt), xdrIndicators(rt), xdrHunt(rt), xdrTimeline(rt),
		detectionsCommand(rt), xdrAnalyzer(rt), incidentsCommand(rt))
	return group
}

// xdrClient is a Defender for Endpoint client for a profile, by name.
func xdrClient(rt *Runtime, name string) (*xdr.Client, microsoft.Profile, error) {
	profile, err := rt.Profile(name)
	if err != nil {
		return nil, profile, err
	}
	return xdrClientFor(rt, profile)
}

func xdrClientFor(rt *Runtime, profile microsoft.Profile) (*xdr.Client, microsoft.Profile, error) {
	api, err := rt.API(profile)
	if err != nil {
		return nil, profile, err
	}
	client, err := xdr.New(api, profile.MDEURL)
	return client, profile, err
}

// endpointHelp is --endpoint's help: Advanced Hunting goes through Graph by default, and
// this sends it to Defender for Endpoint's own API, which the Azure CLI's sign-in can use.
const endpointHelp = "Through the Defender for Endpoint API instead of Graph: device tables only, but the Azure CLI's sign-in can use it."
