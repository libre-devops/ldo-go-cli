# Devices

[Back to the docs](README.md)

Devices across Entra ID, Defender and Intune, by name. A name can be an FQDN or a short
hostname: each lookup tries the FQDN, then the short name. `ldo-go device` works as well as
`ldo-go devices`.

```bash
ldo-go devices check web01,web02,db01                  # in Entra and onboarded to Defender?
ldo-go devices check -f plan.xlsx --column FQDN --sheet "Ring 1" --tag linux-servers
ldo-go devices check -f hosts.txt --group "MDE Pilot Devices" --intune --compliant
ldo-go devices check web01,web02 --device-group "Linux servers"   # a Defender device group
ldo-go devices watch -f hosts.txt --interval 5m --timeout 2h
ldo-go devices watch -f plan.xlsx --column FQDN --where "Scheduled Date=tomorrow" --group "Linux servers" --group "Linux pilot"
ldo-go devices show web01.corp.example.com             # one device, and what looks wrong
ldo-go devices av-signature web01,db01                 # Defender Antivirus versions
ldo-go devices av-signature -f plan.xlsx --column FQDN --at-least 1.419.120.0
```

## check and watch

`check` is one fast pass over the list, looking devices up in parallel (`--workers`, default
8). `watch` repeats it every `--interval` until every device meets every expectation, or
`--timeout` or `--max-passes` stops it; devices already complete are skipped unless you pass
`--recheck`. Progress goes to stderr, the final table to stdout. Both exit 3 when a device
falls short, and `watch` 130 on Ctrl-C, after showing the last pass.

The table has a row a device, in the order given: its name, MET (how many of its checks it
meets, `4/4` when complete), then a column for each expectation, `ok` or what is missing.
`--sort met:desc` puts the complete devices first and `--sort met` the ones to chase first;
any other column sorts too (`--sort defender`). `-o csv` and `-o json` carry the same, for a
spreadsheet or a script.

```bash
ldo-go devices watch -f plan.xlsx --column FQDN --where "Scheduled Date=today" \
  --group "Linux servers" --interval 5m --timeout 4h --sort met:desc
```

To look on a schedule rather than in a terminal (from cron, or a pipeline's schedule), run
`check`, which looks once: exit 3 says something is still short, and `-o csv` or `-o json`
keeps the detail. The Azure CLI's sign-in must be current where it runs.

| Expectation | Meaning |
| --- | --- |
| `--entra` / `--no-entra` | in Entra ID (on by default) |
| `--defender` / `--no-defender` | onboarded to Defender (on by default) |
| `--active` | Defender health is Active |
| `--tag NAME` | carries this Defender machine tag (repeatable) |
| `--device-group NAME` | falls in this Defender device group (repeatable) |
| `--group NAME_OR_ID` | a member of this Entra group, by display name or object id (repeatable) |
| `--intune`, `--compliant` | enrolled in Intune, and compliant |

A plan in a spreadsheet can say which day each server changes. `--where` keeps only that
day's rows (`--where "Scheduled Date=today"`), and other columns can narrow it further; see
[lists of names](configuration.md#options-every-command-takes). With two `--group`s, a device
must be in both.

## show

One device's Entra objects and groups, Defender record and (with `--intune`) Intune record,
side by side, with what looks wrong: duplicate registrations, a disabled object, Defender not
onboarded, inactive or not seen for `--stale-after` (default 7d), or Defender or Intune
pointing at a different Entra device. Exits 3 on any warning.

## av-signature

Each device's Defender Antivirus signature, engine and platform versions, its mode (active,
passive or EDR block) and Defender's own "definitions up to date" check, from one built-in
Advanced Hunting query. It runs through Graph, or with `--endpoint` through the Defender for
Endpoint API, which the Azure CLI's sign-in can use. `--at-least` flags older signatures, and
`--show-query` prints the KQL to paste into the portal instead. Exits 3 when a device is not
found, is out of date, or is older than `--at-least`.
