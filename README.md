<div align="center">

# ldo-go

The Go port of the Libre DevOps Helpers command line: one binary for day-to-day security and
platform work.

[![Lint and Test](https://github.com/libre-devops/ldo-go-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/libre-devops/ldo-go-cli/actions/workflows/ci.yml)
[![CodeQL](https://github.com/libre-devops/ldo-go-cli/actions/workflows/codeql.yml/badge.svg)](https://github.com/libre-devops/ldo-go-cli/actions/workflows/codeql.yml)
[![Release](https://img.shields.io/github/v/release/libre-devops/ldo-go-cli?label=release&color=1793D1)](https://github.com/libre-devops/ldo-go-cli/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/libre-devops/ldo-go-cli?logo=go&logoColor=white)](go.mod)
[![Licence: MIT](https://img.shields.io/badge/licence-MIT-blue.svg)](LICENSE)

</div>

---

`ldo-go` is a fast, read-only command line for Microsoft (Entra ID, Defender XDR, Intune,
Azure, Graph, PIM, Logic Apps), ServiceNow, Jira and Confluence, with helpers for Terraform
modules. It signs in as you, and can read only what you can.

It is the Go port of [`ldo`](https://github.com/libre-devops/python-helpers), the Python
original, and an experiment in how far a port gets: the Python `ldo` is the reference. The
two share a config file and behave the same, command for command, and every way they differ
is written down in [How it differs](docs/differences.md).

## Why Go

- **One binary**, with nothing to install first: no Python, no virtual environment.
- **Pure Go sign-in**, through Microsoft's own libraries (azidentity and MSAL): with
  `auth = "device-code"` or `"interactive"`, a person signs in without the Azure CLI at all.
  The Azure CLI is still used by the default `auth = "azure-cli"`, since its sign-in lives in
  its own token cache.

## Commands

| Command | What it does | Docs |
| --- | --- | --- |
| `ldo-go devices` | check a list of devices across Entra, Defender and Intune, watch until they are all there, show one, read Defender Antivirus versions | [devices](docs/devices.md) |
| `ldo-go entra` | devices and whether they are in a group, users, groups, roles, sign-ins, app credentials, Conditional Access; tokens | [entra](docs/entra.md) |
| `ldo-go intune` | managed devices: compliance, last sync, owner | [entra](docs/entra.md) |
| `ldo-go xdr` | Defender machines, alerts, vulnerabilities, indicators, Advanced Hunting, a device's timeline, custom detection rules (and their export to YAML), MDE Client Analyzer results | [defender](docs/defender.md) |
| `ldo-go xdr incidents` | the Defender XDR queue, Sentinel's included: top, latest, between days, summary | [defender](docs/defender.md#incidents-sentinels-included) |
| `ldo-go graph` | any Graph GET, objects by name, `whoami`, a Graph token, hunting | [graph](docs/graph.md) |
| `ldo-go azure` | subscriptions, Resource Graph, role assignments, Defender for Cloud, splitting resource ids into their parts | [azure](docs/azure.md) |
| `ldo-go azure automation` | Automation accounts: runbook jobs, and each job's logs and output | [azure](docs/azure.md#automation) |
| `ldo-go keyvault` | secrets, certificates and keys close to expiry | [azure](docs/azure.md#key-vault) |
| `ldo-go logs` | KQL against a Log Analytics or Sentinel workspace, and which tables are receiving data | [azure](docs/azure.md#log-analytics) |
| `ldo-go pim` | eligible, active and standing access, requests, approvals, activation settings | [pim](docs/pim.md) |
| `ldo-go logicapp` | offline checks, export and validation for Consumption Logic Apps and Sentinel playbooks | [logic apps](docs/logic-apps.md) |
| `ldo-go snow` | ServiceNow: sign in, whoami, the instance, applications, a token | [servicenow](docs/servicenow.md) |
| `ldo-go news` | Microsoft 365 Message Center: posts by date, service and category, one post as Markdown | [message center](docs/message-center.md) |
| `ldo-go planner` | Microsoft Planner: plans, buckets, tasks, and a task for each Message Center post, or one a month summing them up | [message center](docs/message-center.md#raising-tasks) |
| `ldo-go jira` | Jira Cloud: issues by JQL or project, one issue with its description as Markdown, projects | [atlassian](docs/atlassian.md) |
| `ldo-go confluence` | Confluence Cloud: spaces, pages, one page as Markdown, CQL search | [atlassian](docs/atlassian.md) |
| `ldo-go terraform` | a Terraform module's variables and outputs in name order, and its README from HEADER.md and terraform-docs | [terraform](docs/terraform.md) |
| `ldo-go az` | switch the Azure CLI between profiles | [signing in](docs/authentication.md) |
| `ldo-go network test` | test the way out through a corporate proxy: the proxy, the certificates, each service | [network](docs/network.md) |
| `ldo-go json` | pretty-print any JSON (`az rest ... \| ldo-go json`) in colour, or as YAML | [configuration](docs/configuration.md#json-yaml-and-logs) |
| `ldo-go profiles`, `ldo-go config` | your profiles, and the config file | [configuration](docs/configuration.md) |

Every data command takes `-p` for a profile and `-o table|json|csv|tsv|html`, lists take
`--sort` and `--unique` by column, and lists of names come from arguments, stdin, a text
file, or a column of a CSV or Excel workbook.

## Install

From a [release](https://github.com/libre-devops/ldo-go-cli/releases): one binary for Linux,
macOS or Windows, on amd64 or arm64. Check it against `SHA256SUMS`, and its build provenance
with `gh attestation verify`:

```bash
gh release download --repo libre-devops/ldo-go-cli --pattern ldo-go-linux-amd64 --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify ldo-go-linux-amd64 --repo libre-devops/ldo-go-cli
install -m 0755 ldo-go-linux-amd64 ~/.local/bin/ldo-go
```

With Go:

```bash
go install github.com/libre-devops/ldo-go-cli/cmd/ldo-go@latest
```

Or from the source, with [just](https://github.com/casey/just): `just build` writes
`bin/ldo-go`. The Azure CLI is needed only for `azure-cli` profiles, and terraform-docs only
for `ldo-go terraform docs`.

## Quickstart

```bash
ldo-go config init             # write a config file to fill in, if you have none yet
ldo-go profiles                # your profiles, which is active, and which can sign in
ldo-go graph whoami            # who you are, and what your token may do
ldo-go devices check web01,web02
ldo-go network test            # behind a corporate proxy? test the way out first
```

The Python `ldo` and `ldo-go` read the same config file, so a profile set up for one works
for the other. Each keeps its own sign-ins.

## Documentation

Everything is in [docs](docs/README.md): [configuration](docs/configuration.md),
[signing in](docs/authentication.md), [permissions](docs/permissions.md),
[proxies and certificates](docs/network.md), a page for each group of commands, and
[how it differs from the Python ldo](docs/differences.md).

## Development

`just check` runs gofmt, go vet and the tests with a coverage floor; `just ci` adds
staticcheck and govulncheck, as CI runs them. Nothing in a test touches the network, a real
`az` or a real clock. [Development](docs/development.md) covers the layout, the tests and
their fakes, the self-test in a real tenant, CI and releasing; [CONTRIBUTING.md](CONTRIBUTING.md)
and [AI.md](AI.md) the conventions. The changes are in the [changelog](CHANGELOG.md).

## Licence

[MIT](LICENSE).
