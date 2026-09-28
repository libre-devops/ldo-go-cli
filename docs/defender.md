# Defender XDR

[Back to the docs](README.md)

## Machines, alerts and vulnerabilities

```bash
ldo-go xdr machines web01 web02 --all-records      # onboarding, health, last seen, tags, device group
ldo-go xdr stale --older-than 30d                  # machines Defender has not seen lately
ldo-go xdr alerts --since 24h --severity medium
ldo-go xdr alerts --device web01 --include-resolved
ldo-go xdr vulns web01 --severity high
ldo-go xdr indicators                              # custom indicators of compromise
```

## Hunting

```bash
ldo-go xdr hunt "DeviceInfo | take 10"
ldo-go xdr hunt "EmailEvents | where Timestamp > ago(1d) | take 50" --timespan 7d
ldo-go xdr hunt --file queries/failed-logons.kql -o csv > failed-logons.csv
ldo-go xdr hunt "DeviceInfo | take 10" --endpoint  # the device tables, with the Azure CLI's sign-in
```

`xdr hunt` (and `graph hunt`, the same thing) runs through Microsoft Graph, which covers every
Defender XDR table: devices, email, identity, cloud apps and alerts. It needs
`ThreatHunting.Read.All`, which the Azure CLI's token never has: use a profile with
[your own app registration](authentication.md#your-own-app-registration). `--endpoint` uses the
Defender for Endpoint API instead: device tables only, but your Defender role is enough.

## A device's timeline

```bash
ldo-go xdr timeline web01 --since 6h
ldo-go xdr timeline web01.corp.example --from 2026-09-24T09:00 --to 2026-09-24T12:30
ldo-go xdr timeline web01 --today --type process,network --endpoint
ldo-go xdr timeline web01 --since 2h -o csv > web01-timeline.csv
ldo-go xdr timeline web01 --type logon --show-query   # the KQL, to run or change yourself
```

**Defender has no API for the device timeline.** The timeline page in the portal reads an
internal service of the portal's own, which Microsoft neither documents nor supports, and
which accepts no token an app registration or the Azure CLI can get. Tools that call it
borrow a browser's session, which `ldo-go` will not do.

What it can do is ask Advanced Hunting, which holds the events the timeline is drawn from,
in its device tables. `xdr timeline` runs one query over them for one device and one window
of time, and lists the events newest first, each as TIME, TYPE, ACTION, ACCOUNT, PROCESS and
DETAIL:

| `--type` | Table | DETAIL |
| --- | --- | --- |
| `process` | `DeviceProcessEvents` | the command line |
| `network` | `DeviceNetworkEvents` | the remote address and port, and URL, or what is listening |
| `file` | `DeviceFileEvents` | the file's path |
| `registry` | `DeviceRegistryEvents` | the key, and the value set |
| `logon` | `DeviceLogonEvents` | the logon type, and where from |
| `image-load` | `DeviceImageLoadEvents` | the DLL or library loaded |
| `other` | `DeviceEvents` | antivirus detections, exploit protection and the rest |
| `alert` | `AlertEvidence` and `AlertInfo` | the alert's title, its severity as the ACTION |

How it differs from the portal's timeline:

- **30 days.** Advanced Hunting keeps 30 days of device events; the portal reaches further
  back. A window older than that is warned about, since those events are not there.
- **The newest 1000 by default.** A query returns at most 100,000 rows, and each one counts
  against the tenant's hunting quota. A busy server writes thousands of events an hour, so
  narrow the window or `--type` before you raise `--limit`; a timeline that stopped short
  says so.
- **Just the events.** What the portal adds on top (events flagged on the timeline, grouped
  related events, techniques shown inline) is not in the tables.
- **One name, maybe more than one device.** A rebuilt server keeps its name and gets a new
  id. When events come from more than one device, it says so, and `-o json` has each event's
  `device_id`. A host name on its own (`web01`) also finds `web01.corp.example`, never
  `web010`.

Times show in local time; `-o json` has them in UTC, as the tables keep them. Like `xdr
hunt`, it goes through Graph (`ThreatHunting.Read.All`) unless you pass `--endpoint`, which
works with the Azure CLI's sign-in. The Defender for Endpoint API has the device tables but
not the alert ones, so with `--endpoint` alerts are left out (a note says so), and `--type
alert --endpoint` is refused. `--from` and `--to` take a day, or a day and a time (local, or
UTC with a `Z`); `--today`, `--yesterday` and `--since` work too.

## Incidents, Sentinel's included

In the unified security operations platform, Sentinel's incidents land in the same queue as
Defender's, so these read both:

```bash
ldo-go xdr incidents top                           # today's open incidents, most severe first
ldo-go xdr incidents top --severity high -n 20
ldo-go xdr incidents latest --source sentinel      # the newest, any status, last 30 days
ldo-go xdr incidents list --from 2026-09-01 --to 2026-09-24
ldo-go xdr incidents list --from 2026-09-24T09:00 --to 2026-09-24T12:00   # or between times
ldo-go xdr incidents list --yesterday --status resolved
ldo-go xdr incidents list --since 6h --updated     # updated, not created, in the last 6 hours
ldo-go xdr incidents summary --since 7d            # counts by severity, status and source
ldo-go xdr incidents show 12345                    # alerts, devices, users and the portal link
```

| Option | Values |
| --- | --- |
| window | `--today`, `--yesterday`, `--since 7d`, or `--from` and `--to` (a day, `YYYY-MM-DD`, `today` or `yesterday`, whole; or a moment, `YYYY-MM-DDTHH:MM`, local or with a `Z` for UTC); `--updated` windows on the last update |
| `--status` | `open`, `active`, `in-progress`, `awaiting-action`, `resolved`, `redirected`, `all` (repeatable) |
| `--severity` | at least `informational`, `low`, `medium` or `high` |
| `--source` | `sentinel`, `endpoint`, `identity`, `office`, `cloud-apps`, `cloud`, `xdr`, `entra`, `app-governance`, `dlp`, `insider-risk` (repeatable) |
| `-n`, `--limit` | how many |

Incidents need `SecurityIncident.Read.All` (the Azure CLI's token never has it) and a role
such as Security Reader. A Sentinel workspace not onboarded to the Defender portal keeps its
incidents to itself: read its `SecurityIncident` table with `ldo-go logs query`.

## Custom detection rules

```bash
ldo-go xdr detections list                          # every rule: status, schedule, severity, tactic
ldo-go xdr detections list --status autoDisabled    # the ones Defender turned off itself
ldo-go xdr detections show "Certutil used to download remote content"
ldo-go xdr detections show 7506 --yaml > certutil.yaml
ldo-go xdr detections export ./custom-detections   # every rule, as YAML files for Terraform
ldo-go xdr detections export ./backup --no-id      # a backup that creates the rules anew
```

With Sentinel run from the Defender portal, detections live there as custom detection rules:
an Advanced Hunting query on a schedule, and the alert (and any automated response) it
raises. The old Sentinel REST API's analytics rules belong to the Azure portal it is leaving,
so `ldo-go` reads these through Microsoft Graph (`security/rules/detectionRules`), which is still
beta only.

**What the API no longer says.** On 1 October 2026 Microsoft removes the legacy properties
(`isEnabled`, `detectorId`, `lastRunDetails` and others), and with `lastRunDetails` goes the
only report of how each run went. `ldo-go` uses none of them. The sign left is a rule's
`status`: `autoDisabled` means Defender turned the rule off itself, usually after its query
failed again and again. `list` exits 3 when it finds one.

**Export to YAML.** `export` (and `show --yaml` for one rule) writes the analyst layout of
[terraform-msgraph-xdr-custom-detection-rules](https://github.com/libre-devops/terraform-msgraph-xdr-custom-detection-rules):
one file per rule at `TACTIC/RULE-NAME.yaml`, the tactic being the rule's first, in kebab
case (`command-and-control/`), or `uncategorised/`. It is the conversion LibreDevOpsHelpers'
`Export-LdoCustomDetectionRule` does, and every file is checked in the tests against the
module's own schema:

- The rule's server id is kept, since the module keys rules by it, so `terraform import`
  lines up. `--no-id` leaves it out, for a backup meant to create the rules anew.
- A rule Defender turned off is written `disabled`, since the schema allows only enabled and
  disabled, with a `TODO(export)` comment saying why and to fix its query first.
- A rule written the old way (a schedule period, a category and technique list, impacted
  assets, response actions) is converted where it can be; what cannot be is a `TODO(export)`
  comment, never dropped. So is a rule that maps no entities, which the schema requires.
- A rule carrying automated actions is noted, since the module call then needs
  `allow_automated_actions = true`.
- Files already there are kept unless `--force`, and two rules whose names make the same file
  name both survive (the second gets its id added). Nothing is written through a link or
  outside the folder named, and the tenant is only read.

Rules need `CustomDetection.Read.All` on the Graph token. The Azure CLI cannot ask for it, so
use a profile with [your own app registration](authentication.md#your-own-app-registration),
or have an admin consent the delegated permission for the Azure CLI's own app
(`04b07795-8ddb-461a-bbee-02f9e1bf7b46`).

## MDE Client Analyzer results

Microsoft's [MDE Client Analyzer](https://learn.microsoft.com/defender-endpoint/overview-client-analyzer)
checks one device's Defender for Endpoint (connectivity, services, configuration, versions)
and zips what it finds with everything it collected. `xdr analyzer` reads those zips, one or
many, and lists each device's findings, errors first:

```bash
ldo-go xdr analyzer MDEClientAnalyzerResult.zip                 # one device's findings
ldo-go xdr analyzer results/*.zip --severity warning            # many: only warnings and errors
ldo-go xdr analyzer MDEClientAnalyzerResult.zip --guidance      # with what to do about each
ldo-go xdr analyzer MDEClientAnalyzerResult.zip --facts         # its OS, versions and services
ldo-go xdr analyzer results/*.zip -o json > analyzer.json       # every result in full
```

It takes the zip itself, the folder it unpacks to, or the results XML, from:

- **Windows**, `MDEClientAnalyzer.cmd`: it reads `SystemInfoLogs\MDEClientAnalyzer.xml`,
  whose findings carry their own severity, check, result and guidance.
- **Linux and macOS**, the support tool (`mde_support_tool.sh` or its compiled
  `MDESupportTool`): it reads `mde.xml`. Those findings are ids alone, so `ldo-go` says what
  each means, or uses the tool's own `events.xml` where the zip carries it.

Everything is read here: nothing is sent anywhere, and nothing in the zip is unpacked to
disk (only the results file is read, up to 16 MB). Exits 3 when a device has an error or a
warning.
