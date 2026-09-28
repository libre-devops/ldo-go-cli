package cli

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/network"
	"github.com/libre-devops/ldo-go-cli/internal/core/probe"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
)

// The network commands test the way out, through the proxy and past a TLS-inspecting
// one. network test asks each service this tool uses for something every one answers
// without a sign-in, and expects a 2xx: proof the proxy, the certificates and the route
// all work, the same way every other command's calls go.

// endpoint is a service to try: its name, a URL it answers without a sign-in, the
// statuses that mean it was reached, and a note to show beside a pass.
type endpoint struct {
	name   string
	url    string
	expect func(int) bool
	note   string
}

func networkCommand(rt *Runtime) *cobra.Command {
	group := newGroup("network", "The network: test the proxy, the certificates and the way to each service.")
	group.AddCommand(networkTestCommand(rt))
	return group
}

func networkTestCommand(rt *Runtime) *cobra.Command {
	var common Common
	var urls []string
	var timeout float64
	command := &cobra.Command{
		Use:   "test",
		Short: "Test the way out to each service: the proxy, the certificates and the route.",
		Long: "Test the way out to each service: the proxy, the certificates and the route.\n\n" +
			"Asks Entra ID, Graph, Azure Resource Manager and Defender (in the profile's cloud), and each ServiceNow " +
			"instance configured, for something they " +
			"answer without a sign-in, the same way every other call goes. Says which proxy each went through and, " +
			"when one fails, what to try. Exits 3 when any fails.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			if timeout < 1 || timeout > 120 {
				return Usagef("--timeout", "%g is not in the range 1<=x<=120", timeout)
			}
			endpoints := networkEndpoints(rt, common.Profile)
			for _, item := range urls {
				if !strings.HasPrefix(item, "https://") {
					return Usagef("--url", "%q is not an https URL", item)
				}
				endpoints = append(endpoints, endpoint{name: item, url: item})
			}
			return testNetwork(rt, output, endpoints, time.Duration(timeout*float64(time.Second)))
		},
	}
	command.Flags().StringArrayVar(&urls, "url", nil, "Also test this https URL, expecting a 2xx. Repeatable.")
	command.Flags().Float64Var(&timeout, "timeout", 10, "Seconds to wait for each.")
	common.AddOutput(command, true)
	return command
}

func testNetwork(rt *Runtime, output render.Output, endpoints []endpoint, timeout time.Duration) error {
	settings := rt.Network()
	shown, record, err := networkSettings(settings)
	if err != nil {
		return err
	}
	prober := probe.Prober{Settings: settings, Timeout: timeout}
	if rt.HTTPClient != nil {
		prober.Transport = rt.HTTPClient.Transport
	}
	targets := make([]probe.Target, len(endpoints))
	for index, item := range endpoints {
		targets[index] = probe.Target{URL: item.url, Expect: item.expect}
	}
	results, err := prober.All(rt.Ctx(), targets, 8)
	if err != nil {
		return err
	}
	if output == render.Table {
		rt.Console.Println(pairs(rt, shown))
		rt.Console.Println("")
	}
	rows := make([][]render.Cell, len(results))
	records := make([]any, len(results))
	for index, result := range results {
		rows[index] = probeRow(endpoints[index], result)
		records[index] = probeRecord(endpoints[index], result)
	}
	if err := rt.Console.Emit(output, []string{"ENDPOINT", "VIA", "RESULT", "DETAIL"}, rows,
		map[string]any{"settings": record, "endpoints": records}); err != nil {
		return err
	}
	failed := 0
	for index, result := range results {
		if !result.OK {
			failed++
			if result.Hint != "" {
				rt.Console.Warn("%s: %s", endpoints[index].name, result.Hint)
			}
		}
	}
	rt.Console.Note("%d of %d reachable", len(results)-failed, len(results))
	if failed > 0 {
		return Attention
	}
	return nil
}

// probeRow is ENDPOINT, VIA (the proxy, without its password, or direct), RESULT, DETAIL.
func probeRow(item endpoint, result probe.Result) []render.Cell {
	via := result.Route.Shown()
	switch {
	case result.Route.Proxy == "" && (result.Route.Source == "none" || result.Route.Source == "local"):
		via = "direct"
	case via == "":
		via = "direct (" + result.Route.Source + ")"
	}
	detail := result.Detail
	outcome := render.Coloured("failed", "red")
	if result.OK {
		outcome = render.Coloured("ok", "green")
		detail += fmt.Sprintf(" in %.2fs", result.Seconds)
		if item.note != "" {
			detail += " (" + item.note + ")"
		}
	}
	return []render.Cell{render.Plain(item.name), render.Plain(via), outcome, render.Plain(detail)}
}

func probeRecord(item endpoint, result probe.Result) map[string]any {
	return map[string]any{
		"name": item.name, "url": item.url, "proxy": orNil(result.Route.Shown()), "route": result.Route.Source,
		"ok": result.OK, "status": orNilInt(result.Status), "detail": result.Detail, "hint": orNil(result.Hint),
		"seconds": result.Seconds,
	}
}

// networkEndpoints are the services in the profile's cloud, or the public cloud's when
// there is no profile to go on.
func networkEndpoints(rt *Runtime, profileName string) []endpoint {
	cloud, mdeURL := networkCloud(rt, profileName)
	found := []endpoint{
		{name: "Entra ID sign-in", url: strings.TrimRight(cloud.LoginURL, "/") + "/common/v2.0/.well-known/openid-configuration"},
		{name: "Microsoft Graph", url: strings.TrimRight(cloud.GraphURL, "/") + "/v1.0/"},
		{name: "Azure Resource Manager", url: strings.TrimRight(cloud.ARMURL, "/") + "/metadata/endpoints?api-version=2022-09-01"},
	}
	if mdeURL == "" {
		mdeURL = cloud.MDEURL
	}
	if mdeURL != "" {
		// The Defender API has nothing to show without a token; its 401 proves the way in.
		found = append(found, endpoint{
			name: "Defender for Endpoint", url: strings.TrimRight(mdeURL, "/") + "/api/",
			expect: func(status int) bool { return probe.Success(status) || status == 401 },
			note:   "401 without a token is the expected answer",
		})
	}
	for _, instance := range servicenowInstances(rt) {
		found = append(found, endpoint{
			name: "ServiceNow " + strings.TrimPrefix(instance, "https://"), url: instance + "/",
			expect: func(status int) bool { return status >= 200 && status < 400 },
		})
	}
	return found
}

// servicenowInstances are each ServiceNow instance configured, once each.
func servicenowInstances(rt *Runtime) []string {
	profiles, err := rt.SnowProfiles()
	if err != nil {
		return nil
	}
	var found []string
	for _, profile := range profiles {
		if !slices.Contains(found, profile.Instance) {
			found = append(found, profile.Instance)
		}
	}
	return found
}

// networkCloud is the profile's cloud, and its own Defender URL, when there is one to go
// on; the public cloud otherwise.
func networkCloud(rt *Runtime, name string) (microsoft.Cloud, string) {
	if name == "" {
		name = rt.Env(brand.ProfileEnv)
	}
	var ms *microsoft.Config
	var err error
	if name != "" {
		ms, err = rt.Microsoft()
	} else {
		ms, err = rt.OptionalMicrosoft()
		if ms != nil {
			name = ms.DefaultProfile
		}
	}
	if err == nil && ms != nil && name != "" {
		var profile microsoft.Profile
		if profile, err = ms.Get(name); err == nil {
			return profile.Cloud, profile.MDEURL
		}
	}
	if err != nil {
		rt.Console.Warn("testing the public cloud: %v", err)
	}
	return microsoft.Public, ""
}

// networkSettings are the proxy, the hosts that skip it and the certificates, as pairs to
// show and as a record.
func networkSettings(settings network.Settings) ([][2]string, map[string]any, error) {
	proxy, err := settings.Configured("https")
	if err != nil {
		return nil, nil, err
	}
	var skipped []string
	for _, host := range slices.Concat(network.AlwaysDirect, settings.NoProxyEntries()) {
		if !slices.Contains(skipped, host) {
			skipped = append(skipped, host)
		}
	}
	trust := settings.Trust()
	shownProxy := "none: direct"
	if proxy.Proxy != "" {
		shownProxy = proxy.Shown() + " (" + proxy.Source + ")"
	}
	certificates := "this machine's store"
	source := "combined"
	switch {
	case trust.Explicit != "":
		certificates = trust.Path + ", used as it is (" + trust.Explicit + ")"
		source = trust.Explicit
	case trust.Path != "":
		certificates += fmt.Sprintf(", and %d from ca_bundle (%s)", trust.Certificates, trust.Path)
	}
	shown := [][2]string{{"Proxy", shownProxy}, {"No proxy", strings.Join(skipped, ", ")}, {"Certificates", certificates}}
	record := map[string]any{
		"proxy": orNil(proxy.Shown()), "proxy_source": proxy.Source, "no_proxy": skipped,
		"ca_bundle": orNil(trust.Path), "ca_bundle_source": source,
		"certificates": map[string]any{"system_store": trust.Explicit == "", "extra": trust.Certificates},
	}
	return shown, record, nil
}
