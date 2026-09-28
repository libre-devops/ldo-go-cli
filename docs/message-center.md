# Message Center and Planner

[Back to the docs](README.md)

`ldo-go news` reads the Microsoft 365 Message Center: the posts announcing what changes in the
tenant's services, and by when. `ldo-go planner` reads Planner, and raises a task for each post
a plan does not have one for yet, so a team can work through them on a board, or one task a
month summing up that month's posts.

```bash
ldo-go news messages --since 7d                       # the posts changed this week
ldo-go news messages --security                       # only Defender, Sentinel, Purview, Entra, Intune
ldo-go news messages --service xdr --date today       # a service by part of its name, and one day
ldo-go news messages --date 2026-09-01..2026-09-14 --major
ldo-go news message MC1183010                         # one post, and all it says, as Markdown
ldo-go news message MC1183010 --markdown > MC1183010.md
ldo-go planner plans                                  # your plans
ldo-go planner buckets "SOC changes"                  # a plan's buckets: the columns of its board
ldo-go planner tasks "SOC changes" --open             # its tasks not complete
ldo-go planner add-news "SOC changes" --bucket "To be discussed" --security            # what it would raise
ldo-go planner add-news "SOC changes" --bucket "To be discussed" --security --write    # raises them
ldo-go planner add-rollup "SOC changes" --bucket "To be discussed"            # this month's summary
ldo-go planner add-rollup "SOC changes" --bucket "To be discussed" --write    # raises or updates it
ldo-go planner add-rollup "SOC changes" --bucket "To be discussed" --date 2026-06-01..2026-08-31
```

## Signing in

Message Center needs Microsoft Graph's `ServiceMessage.Read.All`, and raising tasks
`Tasks.ReadWrite`. Both are delegated scopes: they act as you, so `ldo-go` reads only what you
may, and raises tasks only in plans you are a member of. The Azure CLI's token has neither,
so register an app of your own for them, once per tenant, and have an administrator
consent to it:

```bash
app=$(az ad app create --display-name "ldo (Message Center and Planner)" \
  --public-client-redirect-uris http://localhost --is-fallback-public-client true \
  --query appId -o tsv)
graph=00000003-0000-0000-c000-000000000000       # Microsoft Graph
for scope in ServiceMessage.Read.All Tasks.ReadWrite; do
  id=$(az ad sp show --id $graph --query "oauth2PermissionScopes[?value=='$scope'].id" -o tsv)
  az ad app permission add --id "$app" --api $graph --api-permissions "$id=Scope"
done
az ad app permission admin-consent --id "$app"   # or an administrator, in Enterprise applications
echo "client_id = \"$app\""
```

Then add a profile that signs in with it to the config file (`ldo-go config path` says where
that is):

```toml
[microsoft.profiles.me]
tenant_id = "<tenant guid>"
auth = "device-code"            # or "interactive", which opens a browser
client_id = "<the app id printed above>"
```

and pass it with `-p`:

```bash
ldo-go news messages --security -p me
ldo-go planner add-news "SOC changes" --bucket "To be discussed" --security -p me
```

The first command signs you in (with `device-code`, a code to enter at
microsoft.com/devicelogin) and keeps the sign-in, so the next ones do not ask again: see
[keeping a sign-in](authentication.md#keeping-a-sign-in). The same app can carry the scopes
PIM, incidents and hunting need as well: [your own app
registration](authentication.md#your-own-app-registration) lists them all.

Reading plans and tasks works with the Azure CLI's sign-in too (its `Group.ReadWrite.All`
covers group plans). Planner needs a licence that includes it: without one, Graph answers
that the tenant has it disabled. Graph covers basic plans, not premium ones.

## Choosing posts

Every `news` command, `planner add-news` and `planner add-rollup` take the same filters:

| Option | Keeps the posts |
| --- | --- |
| `--date` | changed then: `today`, `29/09/2026`, `2026-09-01..2026-09-14`, `last 7d`, or a length of time such as `7d`. Days are read as [`--where`](configuration.md#options-every-command-takes) reads them |
| `--since` | changed in that length of time, e.g. `7d`, `36h` (the default: 30 days for `news`, 7 for `add-news` and `add-rollup`) |
| `--service` | for a service whose name holds this, ignoring case: `xdr` is Microsoft Defender XDR. Repeatable |
| `--security` | for the security services: Microsoft Defender (every product), Sentinel, Purview, Entra and Intune |
| `--category` | in one of Message Center's categories: plan for change, stay informed, or prevent or fix issue |
| `--major` | that are major changes |

## Raising tasks

`planner add-news PLAN --bucket NAME` looks at every task in the plan, in any bucket and done
or not, for one whose title holds the post's id in square brackets
(`[Microsoft Teams] ... [MC1183010]`, as Microsoft's own Message Center sync to Planner
titles them) or starts with it (`MC1183010: ...`): a post has a task already when one does,
so a board that sync fills, or that tasks were moved to from one, gets no second task for a
post. Without `--write` it only says which it would raise, and exits 3 when there are any;
run it weekly, look, then run it again with `--write`.

Each task it raises goes in that bucket, laid out as Microsoft's sync lays them out, so the
two look alike on a board:

```text
[Microsoft Defender XDR] Microsoft Defender for Endpoint: a new setting [MC1183010]

Message ID: MC1183010
Published date: 9/21/2026
Category: Stay informed
Tags: Admin impact, Feature update

https://admin.microsoft.com/#/MessageCenter/:/messages/MC1183010

The post's text, as Markdown.
```

The date is the day the post was published, month first as the sync writes it. A title
longer than Planner takes (255 characters) loses the end of the post's title, never its id.
`--layout short` titles a task `MC1183010:` and the post's title instead, with the link and
the text as its notes. Planner's labels and checklists are left as they are, for the team to
set.

## Monthly rollups

`planner add-rollup PLAN --bucket NAME` sums up each month's posts in one task, titled
`Message Center rollup: 2026-09 (12 messages)`. Its description counts the month's posts by
severity, service and category, then lists them one a line, newest change first:

```text
# Message Center summary (2026-09)

Total: 12 messages (0 critical, 1 high, 11 normal)

## By service
- Microsoft Teams: 5
...

## Messages
- MC1183010 2026-09-26 [Microsoft Teams] Microsoft Teams: ...
```

A month's rollup holds every post last changed in that month, the whole month and not only
the days asked for: the dates only choose the months, the last 7 days' by default, so a
weekly run keeps this month's rollup up to date and, in a month's first week, finishes last
month's. A month with a rollup already (a task in the plan, in any bucket and done or not,
whose title starts `Message Center rollup:` and the month) has it brought up to date rather
than raised again, its progress left as it is; one that says the same already is left alone.
A post changed again in a later month is in that month's rollup too, and one Microsoft has
since taken out of Message Center is in none: either way, a line a rollup had for it before
is kept, under `## Listed before`, so bringing a rollup up to date loses nothing it listed.
Without `--write` it only says which it would raise or update, and exits 3 when there are
any.

Raising and updating these tasks are the only changes `ldo-go` makes to a tenant, and only with
`--write`.
