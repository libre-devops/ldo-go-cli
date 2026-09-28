package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft/xdr"
)

var severityColours = map[string]string{"critical": "red", "high": "red", "medium": "yellow"}

func severityCell(severity string) render.Cell {
	return render.Coloured(severity, severityColours[strings.ToLower(severity)])
}

func xdrAlerts(rt *Runtime) *cobra.Command {
	common := &Common{}
	var device, since, severity string
	var includeResolved bool
	var limit int
	command := &cobra.Command{
		Use:   "alerts",
		Short: "List Defender alerts, newest first: open ones unless --include-resolved.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := positive(cmd, "limit", limit); err != nil {
				return err
			}
			if severity != "" {
				if _, err := xdr.ParseSeverity(severity); err != nil {
					return err
				}
			}
			window, err := Duration("--since", since)
			if err != nil {
				return err
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := xdrClient(rt, common.Profile)
			if err != nil {
				return err
			}
			query := xdr.AlertQuery{Since: rt.Clock().Add(-window), MinSeverity: severity, IncludeResolved: includeResolved, Limit: limit}
			if device != "" {
				lookup, err := client.FindMachine(rt.Ctx(), device)
				if err != nil {
					return err
				}
				if lookup.Machine() == nil {
					return errs.NotFoundf("no Defender record for '%s'", device)
				}
				query.MachineID = lookup.Machine().ID
			}
			found, err := client.Alerts(rt.Ctx(), query)
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []fields.Object{}
			for index, alert := range found {
				rows[index] = []render.Cell{render.Plain(rt.Console.When(alert.Created)), severityCell(alert.Severity), render.Plain(alert.Status),
					render.Plain(alert.Title), render.Plain(alert.ComputerDNSName), render.Plain(alert.Category),
					render.Plain(alert.DetectionSource), render.Plain(alert.ID)}
				records = append(records, alert.Raw)
			}
			if err := rt.Console.Emit(output, []string{"CREATED", "SEVERITY", "STATUS", "TITLE", "DEVICE", "CATEGORY", "SOURCE", "ALERT ID"},
				rows, records); err != nil {
				return err
			}
			rt.Console.Note("%d alert(s) in the last %s", len(found), util.FormatSpan(window))
			return nil
		},
	}
	flags := command.Flags()
	flags.StringVarP(&device, "device", "d", "", "Only alerts for this device name.")
	flags.StringVar(&since, "since", "7d", "How far back, e.g. 24h, 7d.")
	flags.StringVar(&severity, "severity", "", "At least this severity: low, medium or high.")
	flags.BoolVar(&includeResolved, "include-resolved", false, "Include resolved alerts.")
	flags.IntVar(&limit, "limit", 200, "Most alerts to show.")
	common.AddOutput(command, true)
	return command
}

func xdrVulns(rt *Runtime) *cobra.Command {
	common := &Common{}
	var severity string
	command := &cobra.Command{
		Use:   "vulns DEVICE",
		Short: "List the vulnerabilities Defender reports on a device, most severe first.",
		Long:  "List the vulnerabilities Defender reports on a device, most severe first. DEVICE is its FQDN or short hostname.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			floor := -1
			if severity != "" {
				rank, err := xdr.ParseSeverity(severity)
				if err != nil {
					return err
				}
				floor = rank
			}
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := xdrClient(rt, common.Profile)
			if err != nil {
				return err
			}
			lookup, err := client.FindMachine(rt.Ctx(), args[0])
			if err != nil {
				return err
			}
			if lookup.Machine() == nil {
				return errs.NotFoundf("no Defender record for '%s'", args[0])
			}
			found, err := client.Vulnerabilities(rt.Ctx(), lookup.Machine().ID)
			if err != nil {
				return err
			}
			var kept []xdr.Vulnerability
			for _, item := range found {
				if xdr.SeverityRank(item.Severity) >= floor {
					kept = append(kept, item)
				}
			}
			return showVulns(rt, output, kept, args[0])
		},
	}
	command.Flags().StringVar(&severity, "severity", "", "At least this severity: low, medium, high, critical.")
	common.AddOutput(command, true)
	return command
}

func showVulns(rt *Runtime, output render.Output, found []xdr.Vulnerability, device string) error {
	rows := make([][]render.Cell, len(found))
	records := []fields.Object{}
	for index, item := range found {
		score := ""
		if item.CVSS != nil {
			score = fmt.Sprintf("%.1f", *item.CVSS)
		}
		exploit := ""
		switch {
		case item.ExploitVerified:
			exploit = "verified"
		case item.PublicExploit:
			exploit = "public"
		}
		rows[index] = []render.Cell{render.Plain(item.ID), severityCell(item.Severity), render.Plain(score), render.Plain(exploit),
			render.Plain(rt.Console.When(item.Published)), render.Plain(item.Name)}
		records = append(records, item.Raw)
	}
	if err := rt.Console.Emit(output, []string{"CVE", "SEVERITY", "CVSS", "EXPLOIT", "PUBLISHED", "NAME"}, rows, records); err != nil {
		return err
	}
	noun := "vulnerabilities"
	if len(found) == 1 {
		noun = "vulnerability"
	}
	rt.Console.Note("%d %s on %s", len(found), noun, device)
	return nil
}

func xdrIndicators(rt *Runtime) *cobra.Command {
	common := &Common{}
	command := &cobra.Command{
		Use:   "indicators",
		Short: "List custom indicators of compromise (hashes, IPs, URLs, domains, certificates).",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			output, err := common.Output(rt)
			if err != nil {
				return err
			}
			client, _, err := xdrClient(rt, common.Profile)
			if err != nil {
				return err
			}
			found, err := client.Indicators(rt.Ctx())
			if err != nil {
				return err
			}
			rows := make([][]render.Cell, len(found))
			records := []fields.Object{}
			for index, item := range found {
				rows[index] = render.Cells(item.IndicatorType, item.Value, item.Action, item.Severity, item.Title, rt.Console.When(item.Expires), item.CreatedBy)
				records = append(records, item.Raw)
			}
			return rt.Console.Emit(output, []string{"TYPE", "VALUE", "ACTION", "SEVERITY", "TITLE", "EXPIRES", "CREATED BY"}, rows, records)
		},
	}
	common.AddOutput(command, true)
	return command
}
