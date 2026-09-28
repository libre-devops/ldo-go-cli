# Configuration

[Back to the docs](README.md)

## The config file

```bash
ldo-go config init     # writes the template
ldo-go config path     # where it is
ldo-go profiles        # what it holds, which profile is active, and whether each can sign in
```

It is the Python `ldo`'s own file, at `~/.config/ldo/config.toml` (`%APPDATA%\ldo` on
Windows), or wherever `--config` or `LDO_CONFIG` points, so a profile set up for one works
for the other. It is created readable only by you, has a section per vendor, and never holds
a secret. Unknown keys are rejected, so a typo fails loudly.

```toml
# proxy = "127.0.0.1:3128"             # behind a corporate proxy, e.g. cntlm (see network.md)
# ca_bundle = "~/certs/proxy-ca.pem"   # a TLS-inspecting proxy's root, when not in the OS store

[microsoft]
default_profile = "prod-tenant"

[microsoft.profiles.prod-tenant]
description = "Production tenant"
tenant_id = "<tenant guid>"
workspace = "law-soc"            # the default for 'logs': its name, resource id or Workspace ID

[microsoft.profiles.prod]
tenant_id = "<tenant guid>"
subscription_id = "<subscription guid>"

[servicenow.profiles.work]
instance = "https://itsm.corp.example"
client_id = "<client id>"

[atlassian.profiles.work]
site = "contoso"
email = "ana@corp.example"
```

## Microsoft profiles

A profile is a tenant, optionally pinned to a subscription, with one way of getting tokens.

| Key | Meaning |
| --- | --- |
| `tenant_id` | Required. The tenant the profile acts in. |
| `subscription_id` | Pins the profile to one subscription; Azure commands then cover only it. |
| `auth` | How it signs in: `azure-cli` (the default), `interactive`, `device-code`, `client-secret`, `workload-identity` or `managed-identity`. See [Signing in](authentication.md). |
| `client_id` | The app registration, or user-assigned managed identity, to sign in with. |
| `token_cache` | Where an `interactive` or `device-code` profile keeps its sign-in: `file` (the default) or `memory`; `keychain` is taken, and kept in the file. See [Keeping a sign-in](authentication.md#keeping-a-sign-in). |
| `cloud` | `public` (the default), `usgov` (GCC High) or `china`. Point the Azure CLI at the same one with `az cloud set`. |
| `workspace` | The Log Analytics workspace for `logs` without `--workspace`: its name, its resource id or its Workspace ID (see [Log Analytics](azure.md#log-analytics)). |
| `workspace_id` | The older key for the same, which takes the Workspace ID (a GUID) only. A profile sets one of the two. |
| `mde_url` | A regional Defender for Endpoint endpoint, e.g. `https://api-eu.securitycenter.microsoft.com`. |
| `description` | Shown by `profiles`. |

ServiceNow profiles are described in [ServiceNow](servicenow.md#configuration), and
Atlassian's in [Jira and Confluence](atlassian.md#signing-in). The top-level `proxy`,
`no_proxy` and `ca_bundle` apply to every call, and to the Azure CLI; see
[Proxies and certificates](network.md).

## Options every command takes

| Option | Meaning |
| --- | --- |
| `-p`, `--profile` | The profile, or `LDO_PROFILE`. Without it: the section's `default_profile`, then (for Microsoft) the Azure CLI's active account. |
| `-o`, `--output` | `table` (the default), `json` (the services' records, for `jq`), `csv` (with a header, for spreadsheets), `tsv` (values only, no header, as `az -o tsv`, for `cut` and `while read`) or `html` (a page to open or share: see [HTML reports](#html-reports)). Data goes to stdout; notes and progress to stderr, so `-o csv > file.csv` writes a clean file. |
| `--sort COLUMN[:desc]` | Sort the rows by a column, named as the table heads it (case, spaces and underscores do not matter: `"last seen"`, `last_seen`). Repeat it to sort by more, most significant first. Numbers, versions, severities (`Low` to `Critical`) and dates sort as such, and blanks go last either way. On every list, for the table, CSV and TSV. |
| `--unique COLUMN` | Keep only the first row for each value of a column, ignoring case; repeat it to keep one of each combination of several. It runs after `--sort`, so `--sort "last seen:desc" --unique device` keeps each device's newest record. For JSON, use `jq`'s `sort_by` and `unique_by`. |
| `--colour`, `--no-colour` | Colour, or none, whatever the output is (also spelt `--color`, `--no-color`); before the command, e.g. `ldo-go --colour xdr machines web01 \| less -R`. By default colour shows on a terminal, unless `NO_COLOR` is set; `FORCE_COLOR` turns it on. |
| `-v`, `-vv` | Info or debug logging, on stderr. |
| `--log-format`, `--log-level` | `text`, `json` or `otlp` (see [OpenTelemetry logs](#opentelemetry-logs)); and the least a record must be to be written. |

**Lists of names** (devices, machines) come as `"a,b,c"`, several arguments, `-` for stdin,
or `-f` with a file: one name per line, or a column of a CSV or Excel workbook (`.xlsx`,
`.xlsm`, `.xltx`, `.xltm`) named by `--column`. The header may sit below title rows. In a
workbook, `--sheet` picks the tab; without it, the one visible sheet with that column is used.
Values are read as Excel saved them: no formulas are recalculated and no macros run. A date
cell reads as the day it shows, as `2026-09-25`, and a time as `09:00:00`.

`--where "COLUMN=VALUE"` keeps only the rows where another column holds that value, and
`COLUMN!=VALUE` leaves those rows out. Repeat it: values for one column are alternatives
(`Environment=Dev` and `Environment=Test`), and every column named must match. Text is
matched whatever its case. Without `--where`, every row is read.

| Value | Rows whose cell holds |
| --- | --- |
| `today`, `tomorrow`, `yesterday` | that day |
| `2026-09-25`, `25/09/2026` (UK), `09/25/2026` (US) | that day |
| `2026-09-01..2026-09-14` | any day from the first to the last, both included |
| `..2026-09-14`, `today..` | any day up to, or from, that one |
| `last 7d`, `next 7d` | the seven days to today, or today and the six after |
| nothing (`Scheduled Date!=`) | any value at all, so every row with a date |

A day matches whether Excel keeps it as a date or as text written one of those ways, with or
without a time. No other date formats are read.

UK and US dates differ only when both numbers are 12 or under (`01/02/2026`), and this never
guesses. A span says which it is written in when either end has a number over 12
(`01/09/2026..14/09/2026` is UK), and a column says once one of its dates has; a date you give
is read that way. If nothing settles it, or a column holds both, it stops and asks for
`YYYY-MM-DD`. Dates that are real dates in Excel are never in doubt.

```bash
ldo-go devices check web01,web02
ldo-go devices check -f plan.xlsx --column FQDN --sheet "Ring 1"
ldo-go devices watch -f plan.xlsx --column FQDN --where "Scheduled Date=today" --where "Status!=Done"
ldo-go devices check -f plan.xlsx --column FQDN --sheet "Ring 2" --where "Scheduled Date=01/09/2026..14/09/2026"
ldo-go devices check -f plan.xlsx --column FQDN --where "Scheduled Date=last 7d"
cat hosts.txt | ldo-go xdr machines -
ldo-go xdr vulns web01 --sort severity:desc --sort cvss:desc
ldo-go xdr machines -f hosts.txt --sort "last seen:desc" --unique device -o csv > seen.csv
```

**Queries** (`xdr hunt`, `graph hunt`, `azure resource-graph`, `logs query`) come from the
argument, `--file`, or stdin.

## HTML reports

`-o html` writes any data command's table as a web page: to share in a call, attach to a
change record, or read more easily than a wide table in a terminal.

```bash
ldo-go devices check -f plan.xlsx --column FQDN --where "Scheduled Date=today" -o html
ldo-go xdr analyzer results/MDEClientAnalyzerResult.zip -o html > analyzer.html
ldo-go azure automation logs aa-ops --runbook Rotate-Keys -o html > rotate-keys.html
```

In a terminal it writes the page to a file in the current folder, named after the command
and the time, and opens it in your browser (the path is noted when there is no browser to
open); redirected, it goes to stdout like any other output. The page has the command that
made it, counts of the rows that are ok, need attention or failed, the command's notes,
and the table, whose columns sort with a click, with a filter box and a button to copy the
rows shown as CSV. It follows your light or dark setting, and prints cleanly.

Everything is in the one file: no font, style or script is fetched, so it opens offline or
from an email, and its content security policy lets it load nothing and run only its own
code. It is written only when you ask for it, so no other command is any slower.

## JSON, YAML and logs

What `-o json` writes is kept stable: keys are snake_case (a service's own records keep the
service's names), true and false are booleans, and a key is not renamed or dropped without
an entry in the [changelog](../CHANGELOG.md). A test holds every command to it. A record's
keys are written in sorted order (see [How it differs](differences.md#json-output)).

`-o json` is indented, and coloured on a terminal: keys, strings, numbers and booleans each
have a colour, and brackets take the banner's rainbow by how deeply they nest, so a pair
shares one. Piped into `jq` or a file, it is plain JSON. `NO_COLOR` turns the colour off.

`ldo-go json` does the same for JSON from anywhere else, from stdin or a file, and reads JSON
Lines (one document per line) too. Its output is the Python `ldo json`'s, byte for byte,
keeping each document's own key order:

```bash
az rest --url "https://graph.microsoft.com/v1.0/me" | ldo-go json
ldo-go json response.json --sort-keys
ldo-go graph get me -o json | ldo-go json --yaml       # as YAML
ldo-go json events.jsonl --compact                     # one line each
ldo-go json big.json --colour | less -R                # keep the colour through a pager
```

`--yaml` writes YAML, quoting any string a YAML reader could take for something else (`yes`,
`no`, `null`, `1.0`, `2026-09-24`, `@odata.context`), and writing multi-line strings as `|`
blocks. It only converts from JSON; there is no YAML to JSON.

## OpenTelemetry logs

Logs go to stderr, so they never mix with data. `-v` and `-vv` turn them on as text.
`--log-format otlp` (or `LDO_LOG_FORMAT=otlp`) writes OpenTelemetry instead, in the
[OTLP file format](https://opentelemetry.io/docs/specs/otel/protocol/file-exporter/): JSON
Lines, each line a complete OTLP/JSON `LogsData` holding one record. In that mode stderr
holds nothing else: the notes, warnings and errors `ldo-go` would print become records too
(notes at INFO, so `LDO_LOG_LEVEL=info` to keep them), and an error's hint is an attribute.
`--log-format json` writes one flat JSON object a record instead.

```bash
LDO_LOG_FORMAT=otlp LDO_LOG_LEVEL=info ldo-go devices check -f hosts.txt 2>> /var/log/ldo/ldo-go.jsonl
```

| Variable | Becomes |
| --- | --- |
| `LDO_SERVICE_NAME`, else `OTEL_SERVICE_NAME` | the resource's `service.name` (default `ldo-go`) |
| `LDO_SERVICE_VERSION` | `service.version` (default the tool's version) |
| `LDO_DEPLOYMENT_ENVIRONMENT` | `deployment.environment.name` |
| `OTEL_RESOURCE_ATTRIBUTES` | more resource attributes, `key=value,key=value` |
| `LDO_TRACE_ID`, `LDO_SPAN_ID` | every record's `traceId` and `spanId`, so one run's logs join a trace |
| `LDO_CORRELATION_ID` | a `correlation_id` attribute, and the `traceId` when it is a GUID and no trace id is set |

An id that is not hex of the right width (dashes are dropped, so a GUID makes a trace id) is
left out, never sent: a collector rejects a whole payload with a bad one.

An OpenTelemetry Collector reads the file with the `otlp_json_file` receiver. From a
container, whose stdout and stderr the runtime writes to its own log files, read those with
`file_log` and turn the lines back into records with the `otlp_json` connector; it skips the
lines that are not OTLP, such as a table on stdout.

```yaml
receivers:
  otlp_json_file:
    include: [/var/log/ldo/*.jsonl]
exporters:
  otlp:
    endpoint: otel-backend.example.com:4317
service:
  pipelines:
    logs:
      receivers: [otlp_json_file]
      exporters: [otlp]
```

## Environment variables

| Variable | Meaning |
| --- | --- |
| `LDO_CONFIG` | The config file. |
| `LDO_PROFILE`, `LDO_SNOW_PROFILE`, `LDO_ATLASSIAN_PROFILE` | The Microsoft, ServiceNow and Atlassian profile, as `--profile`. |
| `LDO_LOG_FORMAT`, `LDO_LOG_LEVEL` | As `--log-format` and `--log-level`. |
| `LDO_SERVICE_NAME`, `LDO_TRACE_ID` and the rest | What OTLP logs say about themselves; see [OpenTelemetry logs](#opentelemetry-logs). |
| `LDO_TOKEN_CACHE` | Another file for kept sign-ins, e.g. a container volume. |
| `LDO_PROXY_ADDRESS` | The proxy for `ldo-go` (and the Azure CLI it runs), winning over `proxy` and `HTTPS_PROXY`, e.g. `127.0.0.1:3129` for cntlm. |
| `HTTPS_PROXY`, `HTTP_PROXY`, `ALL_PROXY`, `NO_PROXY` | As every tool reads them. See [Proxies and certificates](network.md). |
| `LDO_CA_BUNDLE`, `REQUESTS_CA_BUNDLE`, `CURL_CA_BUNDLE` | A CA bundle to use exactly as it is, in place of the OS store. |
| `LDO_NO_BANNER`, `NO_COLOR`, `FORCE_COLOR` | No banner; no colour; colour even when piped. The banner only ever shows on a terminal. |
| `LDO_RECORD_JSON_OUTPUT` | For the tests: record every command's JSON shape afresh (`just record-json`). |
| `AZURE_CLIENT_SECRET`, `AZURE_FEDERATED_TOKEN_FILE` | For `client-secret` and `workload-identity` profiles, as the Azure SDKs use them. |
| `SNOW_INSTANCE_URL`, `SNOW_CLIENT_ID`, `SNOW_CLIENT_SECRET`, `SNOW_INSTANCE_USERNAME`, `SNOW_INSTANCE_PASSWORD` | ServiceNow, with or without a config file. See [ServiceNow](servicenow.md). |
| `JIRA_INSTANCE`, `JIRA_EMAIL`, `JIRA_TOKEN` | Jira and Confluence: the site, the account, and its API token. See [Jira and Confluence](atlassian.md). |

The Python `ldo`'s `LDO_REAUTH` has no meaning here: see
[When a sign-in lapses](authentication.md#when-a-sign-in-lapses).

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success. |
| 1 | An error, or a token check failed (`--strict` fails on warnings too). |
| 2 | A usage error. |
| 3 | It ran, and found something that needs attention: a device short of an expectation, a watch that hit its limit, a credential close to expiry, an out-of-date signature. |
| 130 | Stopped with Ctrl-C. |

Code 3 lets a scheduled job alert on findings while still failing loudly on errors.
