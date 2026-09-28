# Permissions

[Back to the docs](README.md)

Every command runs with the profile's permissions. The Azure CLI's token is fixed by
Microsoft and covers most of `ldo-go`, but not all of it:

| Commands | API | Needs | Azure CLI's token? |
| --- | --- | --- | --- |
| `entra devices`, `device-groups`, `group-devices`, `group-members`, `user-groups`, `--group` | Graph | directory, device, group or user read | yes |
| `entra user-roles` | Graph | directory read; PIM eligibility needs `RoleEligibilitySchedule.Read.Directory` | active roles only |
| `entra sign-ins` | Graph | `AuditLog.Read.All` and directory read; Entra ID P1 | yes |
| `entra app-credentials` | Graph | `Application.Read.All` | yes |
| `entra ca-policies` | Graph | `Policy.Read.All` | no: Graph returns an empty list |
| `intune devices`, `--intune`, `--compliant` | Graph | `DeviceManagementManagedDevices.Read.All` | no |
| `xdr` machines, alerts, vulns, indicators, `--defender`, `--tag`, `--device-group` | Defender | the read permissions, or a Defender role | yes, through your Defender role |
| `xdr hunt`, `graph hunt`, `devices av-signature` | Graph | `ThreatHunting.Read.All`, and a Defender XDR role | no; `--endpoint` does, for the device tables |
| `xdr incidents ...` | Graph | `SecurityIncident.Read.All`, and a role such as Security Reader | no |
| `xdr detections ...` | Graph (beta) | `CustomDetection.Read.All`, and a Defender XDR role | only when an admin consents it for the Azure CLI's app |
| `graph get ...` | Graph | whatever the path reads | directory, users, groups, devices, apps: yes |
| `azure ...`, `azure automation ...`, `logicapp export` | ARM | Reader | yes |
| `news ...` | Graph | `ServiceMessage.Read.All` | no |
| `planner plans`, `buckets`, `tasks` | Graph | `Tasks.Read`, or `Group.Read.All` for group plans | yes, for group plans |
| `planner add-news --write`, `add-rollup --write` | Graph | `Tasks.ReadWrite` (and `ServiceMessage.Read.All`) | no |
| `jira ...`, `confluence ...` | Atlassian | an API token, reading what its account may in Jira's and Confluence's own permissions | yes |
| `xdr analyzer`, `logicapp check`, `diff`, `params` and the others that read files | none: local files | nothing | yes |
| `logicapp validate` | ARM | `Microsoft.Logic/locations/workflows/validate/action` (Logic App Contributor) | yes |
| `keyvault expiry` | Key Vault | Key Vault Reader, and a firewall that lets you in | yes |
| `logs query` | Log Analytics | Log Analytics Reader | yes |
| `pim ... --azure` | ARM | Reader at the scope; Entra ID P2 | yes |
| `pim ... --entra` | Graph | `RoleEligibilitySchedule.Read.Directory`, `RoleAssignmentSchedule.ReadWrite.Directory`, `RoleManagementPolicy.Read.Directory` | no |
| `pim ... --groups` | Graph | `PrivilegedEligibilitySchedule.Read.AzureADGroup`, `PrivilegedAssignmentSchedule.ReadWrite.AzureADGroup`, `RoleManagementPolicy.Read.AzureADGroup` | no |

For a "no", sign in through [your own app registration](authentication.md#your-own-app-registration)
with an `interactive` or `device-code` profile, or use an automation identity granted the
permission.

## Checking a token

```bash
ldo-go entra token graph                  # which of ldo's features this token covers
ldo-go entra token mde --require Machine.Read.All --strict
pbpaste | ldo-go entra inspect-token -    # a token you already have; nothing is sent anywhere
ldo-go graph whoami                       # who the Graph token is for, and its scopes or roles
```

The checks decode the token and test its expiry, issuer, audience and tenant, then say which
features its scopes or roles cover (a warning rather than an error, since the service's own
RBAC can grant access too). The signature is **not** verified: this answers "is this the token
I meant to get?", not "is this token genuine?". A token is printed only with `--raw`, and never
logged.
