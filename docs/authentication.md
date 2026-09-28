# Signing in

[Back to the docs](README.md)

`ldo-go` works as you: it signs in with your own account and can read only what you can.
Every sign-in goes through Microsoft's own Go libraries: azidentity, and MSAL for a person's
own sign-in.

## As yourself

| `auth` | When | Needs |
| --- | --- | --- |
| `azure-cli` (the default) | almost always: it reuses the sign-in you already have | `az login` |
| `device-code` | no Azure CLI at all, or over SSH, in WSL, in a container | a code to enter at the device login page |
| `interactive` | the same, in a browser on this machine | a browser |

```bash
ldo-go az use prod-tenant                 # switch the Azure CLI to a profile, signing in when needed
ldo-go az use prod-tenant --device-code
ldo-go az whoami                          # the Azure CLI's active account, and the profile it matches
ldo-go entra sign-out -p me               # forget a device-code or interactive profile's kept sign-in
```

Tokens are asked for per tenant, so `az use` is optional: a profile for another tenant works
without switching.

A `device-code` or `interactive` profile needs no Azure CLI: it signs in in pure Go, and
keeps the sign-in (see [Keeping a sign-in](#keeping-a-sign-in)). Without a `client_id` it
signs in as the Azure CLI's own public client, so its token carries the scopes the Azure
CLI's does. PIM on Entra roles and groups, incidents, Graph hunting, and Message Center and
Planner need scopes that token never has: for those, give the profile the `client_id` of
[your own app registration](#your-own-app-registration).

```toml
[microsoft.profiles.me]
tenant_id = "<tenant guid>"
auth = "device-code"            # or "interactive"
client_id = "<your app's id>"   # optional: without it, the Azure CLI's public client
```

## Automation

Unattended jobs can run the same commands with an identity of their own:

| `auth` | For | Needs |
| --- | --- | --- |
| `client-secret` | a job with an app registration and a secret | `client_id`, and the secret in `AZURE_CLIENT_SECRET` |
| `workload-identity` | CI and Kubernetes, with no secret at all | `client_id`, and a federated token from `AZURE_FEDERATED_TOKEN_FILE` or GitHub Actions OIDC |
| `managed-identity` | code on an Azure host | nothing, or `client_id` for a user-assigned identity |

These never lapse: every token is a fresh exchange. The secrets come from the environment,
under the names the Azure SDKs read, and are never taken on the command line or kept in the
config file. In GitHub Actions, give the job `id-token: write` and the app registration a
federated credential for the workflow, then fetch a released binary:

```yaml
permissions:
  id-token: write
  contents: read
steps:
  - run: |
      gh release download --repo libre-devops/ldo-go-cli --pattern ldo-go-linux-amd64 --output /usr/local/bin/ldo-go
      chmod +x /usr/local/bin/ldo-go
    env:
      GH_TOKEN: ${{ github.token }}
  - run: ldo-go --config .github/ldo.toml entra app-credentials -p ci --expiring 30d
```

## When a sign-in lapses

Access tokens last about an hour and are renewed for you, five minutes before they expire,
so a two-hour `devices watch` carries on by itself. A 401 drops the cached token and retries
once. Your sign-in behind them lasts days or weeks, but Entra ID ends it in cases no code can
avoid:

| Cause | Entra ID code | Again? |
| --- | --- | --- |
| a sign-in frequency policy (every 8 hours, say) | AADSTS70043, AADSTS70044 | yes, on that schedule |
| unused for too long (90 days) | AADSTS700082 | only after a long gap |
| a password change or expiry | AADSTS50132, AADSTS50133, AADSTS50055 | no |
| revoked by an administrator, or "sign out everywhere" | AADSTS50173 | no |
| MFA newly required, expired, or not set up | AADSTS50076, AADSTS50078, AADSTS50079 | depends on the policy |

For an `azure-cli` profile, the error names the cause and how to sign in again:
`ldo-go az use <profile>`, or `az login --tenant <tenant>`. Unlike the Python `ldo`,
`ldo-go` does not offer to sign the Azure CLI back in mid-command, so sign in and run the
command again. A `device-code` or `interactive` profile whose kept sign-in no longer works
signs in afresh: a new code to enter, or the browser.

After activating a role in PIM, the Azure CLI may hand out its old token (and old
permissions) for up to an hour; a `device-code` or `interactive` profile starts each command
with a new one.

## Keeping a sign-in

A `device-code` or `interactive` profile keeps its refresh token, so the next command signs
in without asking. Access tokens are never kept. `token_cache` on the profile says where:

| `token_cache` | Kept in | Use it on |
| --- | --- | --- |
| `file` (the default) | `~/.local/state/ldo/ldo-go-sign-ins.json` (`%LOCALAPPDATA%\ldo` on Windows, `~/Library/Application Support/ldo` on macOS), readable only by you | your own machines, headless or not |
| `memory` | nowhere: gone when the command ends | shared machines and jump hosts |

`keychain` is taken too, and the sign-in kept in the file: `ldo-go` has no keychain of its
own. `LDO_TOKEN_CACHE` names another file, such as a container volume.

A refresh token is as good as your sign-in to that app until it expires or is revoked. The
file is the trade the Azure CLI makes on Linux: fine on a machine that is yours. A file other
accounts can read is refused, as ssh refuses a readable key. Two commands running at once
take turns to change it, under a lock file beside it, so neither loses the other's sign-in.
It sits beside the Python `ldo`'s file, not in it, so each tool keeps its own sign-ins.

ServiceNow's kept sign-ins go in the same file: see [ServiceNow](servicenow.md#signing-in).

## Your own app registration

The Azure CLI's token carries the scopes Microsoft chose, and the PIM, incident, hunting,
Message Center and Planner scopes are not among them. Register a public client app once per
tenant, grant it the delegated read scopes, and have an administrator consent:

```bash
app=$(az ad app create --display-name "ldo (delegated sign-in)" \
  --public-client-redirect-uris http://localhost --is-fallback-public-client true \
  --query appId -o tsv)
graph=00000003-0000-0000-c000-000000000000       # Microsoft Graph
arm=797f4846-ba00-4fd7-ba43-dac1f8f63013         # Azure Service Management
for scope in Directory.Read.All RoleEligibilitySchedule.Read.Directory \
  RoleAssignmentSchedule.ReadWrite.Directory RoleManagementPolicy.Read.Directory \
  PrivilegedEligibilitySchedule.Read.AzureADGroup PrivilegedAssignmentSchedule.ReadWrite.AzureADGroup \
  RoleManagementPolicy.Read.AzureADGroup SecurityIncident.Read.All ThreatHunting.Read.All \
  ServiceMessage.Read.All Tasks.ReadWrite; do
  id=$(az ad sp show --id $graph --query "oauth2PermissionScopes[?value=='$scope'].id" -o tsv)
  az ad app permission add --id "$app" --api $graph --api-permissions "$id=Scope"
done
id=$(az ad sp show --id $arm --query "oauth2PermissionScopes[?value=='user_impersonation'].id" -o tsv)
az ad app permission add --id "$app" --api $arm --api-permissions "$id=Scope"
az ad app permission admin-consent --id "$app"
echo "client_id = \"$app\""
```

The same app serves the Python `ldo` and `ldo-go`. Graph insists on a ReadWrite scope even to
list PIM requests; `ldo-go` only ever reads, apart from the Planner writes you ask for with
`--write`.
