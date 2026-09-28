# Azure, Key Vault and Log Analytics

[Back to the docs](README.md)

## Azure

```bash
ldo-go azure subscriptions
ldo-go azure resource-graph "resources | summarize count() by type | order by count_ desc"
ldo-go azure rbac ana@corp.example                  # every role assignment that applies
ldo-go azure secure-score --controls
ldo-go azure recommendations --severity high
ldo-go azure defender-plans
```

Azure commands cover the profile's subscription when it pins one, otherwise every
subscription the credential can see in the tenant; `-s`/`--subscription` (repeatable)
chooses. `rbac` includes assignments made to the principal's groups and those inherited from
management groups.

## Reading a resource id

```bash
ldo-go azure parse-id /subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/rg-net/providers/Microsoft.Network/virtualNetworks/vnet1/subnets/snet-app
ldo-go azure parse-id "$ID" -o json | jq -r '.[0].resource_group_name'
az resource list --query "[].id" -o tsv | ldo-go azure parse-id - -o csv > resources.csv
```

`parse-id` splits Azure Resource Manager (ARM) resource ids into their parts: the
subscription, the resource group, the resource's name and type, the resources above a child
resource (a subnet's virtual network), and what an extension resource (a lock, a role
assignment, a Defender assessment) is on. Management group ids, and ids of subscriptions and
resource groups themselves, read too. It works offline, so the resource need not exist and no
sign-in is needed.

With `-o json` each id comes back with the keys Terraform's
`provider::azurerm::parse_resource_id` gives: `full_resource_type`, `parent_resources`,
`resource_group_name`, `resource_name`, `resource_provider`, `resource_scope`,
`resource_type` and `subscription_id`, plus `id` and `management_group_name`. An id that
cannot be read is named on stderr with what is wrong with it, after the rest are shown, and
the command exits 1.

## Automation

```bash
ldo-go azure automation accounts
ldo-go azure automation jobs aa-ops                        # the newest 20 runs, and how each ended
ldo-go azure automation jobs aa-ops --runbook Rotate-Keys --since 7d
ldo-go azure automation jobs aa-ops --failed               # failed, suspended or stopped
ldo-go azure automation logs aa-ops                        # the newest run's logs, and why it failed
ldo-go azure automation logs aa-ops --runbook Rotate-Keys --stream error --full
ldo-go azure automation logs aa-ops 0b1c2d3e-aaaa-4bbb-8ccc-123456789abc
ldo-go azure automation output aa-ops --runbook Rotate-Keys > rotate-keys.txt
```

An account is named by its name, found across the subscriptions in scope (`-g` narrows it to
a resource group when names repeat), or by its resource id. Without a job id, `logs` and
`output` take the newest job, of `--runbook` when given. `logs` shows the job (its status,
who or what started it, and the exception when it failed) and then every record it wrote,
oldest first: output, warnings and errors, and verbose, progress and debug records when the
runbook turns them on. A listing carries each record's summary; `--full` reads each in full.
`jobs` and `logs` exit 3 when a job shown failed. Jobs are kept for 30 days, and Reader on the
account is enough to read them.

## Key Vault

```bash
ldo-go keyvault expiry kv-app-prd kv-app-dev --within 60d
ldo-go keyvault expiry -f vaults.txt --kind certificate    # the vaults you look after, one a line
```

Metadata only: it never reads a secret's value. Exits 3 when anything expires within the
window. Name the vaults you look after: it has no way to search every vault, on purpose. A
request to each vault you cannot read is refused and logged in that vault, and Defender for
Key Vault can take many of those from one person for reconnaissance.

## Log Analytics

```bash
ldo-go logs query "Heartbeat | summarize arg_max(TimeGenerated, *) by Computer" --timespan 1d
ldo-go logs query --file queries/sign-ins.kql --workspace law-soc -o csv
```

A Sentinel workspace is a Log Analytics workspace, so this reads its tables too. Without
`--workspace`, the profile's `workspace` is used.

A workspace has three names, and it is easy to hand over the wrong one: `--workspace` (and
the profile's `workspace`) takes any of them.

| Name | Looks like | Where to find it |
| --- | --- | --- |
| Workspace ID | a GUID | the workspace's Overview page. The query API wants this one. |
| Resource id | `/subscriptions/.../resourceGroups/rg-soc/providers/Microsoft.OperationalInsights/workspaces/law-soc` | the Overview page's JSON view, or `az monitor log-analytics workspace show` |
| Name | `law-soc` | the portal |

A Workspace ID is used as it is. A resource id or a name is looked up in Resource Manager
first (a name across every subscription the credential can read, which needs Reader on the
workspace), and a note says which workspace it found and its Workspace ID. A name two
workspaces share is refused with both resource ids to choose from. The resource id of
anything else (a resource group, a Key Vault) is refused before anything is sent, saying what
it is the id of.

### Which tables are receiving data

```bash
ldo-go logs ingestion                          # the last 30 days: quiet tables first
ldo-go logs ingestion --quiet-after 6h         # flag a table silent for over six hours
ldo-go logs ingestion --window 90d -o csv > ingestion.csv
```

For each table: when it last received data, how long it has been quiet, and how many GB (and
billable GB) it took in, with the solutions that send it. A data source that stopped sending
shows up here first, quiet at the top, and the command exits 3.

It reads the workspace's `Usage` table, not the tables themselves, so it is cheap to run and
accurate to the hour. That has two limits: a table that received nothing in the whole window
is not listed at all (nothing says it exists, which is why the window defaults to 30 days),
and `Usage` arrives a little behind, so being quiet for under a couple of hours means nothing.
It goes through the Log Analytics query API, as `logs query` does, and needs only Log
Analytics Reader on the workspace.
