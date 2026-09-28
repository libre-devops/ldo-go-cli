# Privileged Identity Management

[Back to the docs](README.md)

```bash
ldo-go pim eligible                                # roles you can activate, in all three areas
ldo-go pim active --permanent-only                 # standing access: active roles with no end date
ldo-go pim requests --pending                      # your requests still waiting
ldo-go pim approvals                               # requests waiting for you to approve
ldo-go pim eligible --azure --user ana@corp.example
ldo-go pim settings "Global Administrator"                         # an Entra role
ldo-go pim settings Owner --scope /subscriptions/<id>              # an Azure role
ldo-go pim settings --group "Platform Admins" --owner              # a PIM for Groups group
```

Each command covers Azure resource roles (`--azure`), Entra roles (`--entra`) and PIM for
Groups (`--groups`): all three unless you pick. An area that cannot be read shows as a
warning with the reason, and the others still show. `settings` shows the longest activation,
whether MFA, a justification, a ticket or approval is needed, and who approves. Nothing here
activates or approves anything.

Azure resource PIM works with the Azure CLI's token. The Entra and group areas need an
`interactive` or `device-code` profile with
[your own app registration](authentication.md#your-own-app-registration), and the tenant
needs Entra ID P2.
