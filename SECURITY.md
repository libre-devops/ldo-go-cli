# Security Policy

## Supported Versions

Only the latest release, and `main`, receive security updates. `ldo-go` is an experimental
port: fixes land on `main` and go out in the next release.

## Scope

`ldo-go` is a command line tool that runs on your own machine or in your CI. It gets access
tokens from the Azure CLI, a person's own sign-in, an app registration or a managed identity,
and makes read-only calls to Microsoft Graph, Defender for Endpoint, Azure Resource Manager,
Key Vault (metadata only), Log Analytics, ServiceNow, Jira and Confluence. There is no
server, no hosted service and no telemetry.

In scope:

- Token handling: any path by which an access token could be printed without `--raw`,
  written to disk, logged, or sent to a host other than the configured API.
- Credentials (`internal/microsoft/identity`): any way for a client secret or federated
  token to be logged, shown in an error, or sent anywhere but the Entra token endpoint.
- Kept sign-ins (`internal/core/tokenstore`): any way for the file to be created readable by
  other accounts or used while it is, for an access token (rather than a refresh token) to
  be kept, or for one profile's sign-in to be used for another cloud, app or tenant. The
  file being readable by the account that owns it, or by root, is the documented trade of
  the option, not a vulnerability.
- ServiceNow sign-in (`internal/servicenow`): any way for a password, client secret or
  token to be logged, printed without `--raw`, or sent to a host other than the profile's
  instance; for the browser sign-in to accept an address from another sign-in (its `state`
  and PKCE checks); and for a kept sign-in to be used for another instance or profile.
- The HTTP client (`internal/core/httpx`), including its host pinning for next links,
  redirect handling and TLS verification, and the network rules (`internal/core/network`):
  the proxy, `no_proxy`, the certificates, and the bundle written for the Azure CLI.
- PIM, Graph and Logic Apps: any way for a command meant to read to change an assignment,
  approve a request, make a request other than a GET (the query endpoints aside), or create
  or change a workflow.
- How external tools are run (`internal/core/process`, `internal/microsoft/azcli`), including
  any way for input to reach a shell or change the arguments passed to a tool.
- Input files (`internal/core/inputs`, `internal/core/sheets`): any way for a text file, CSV
  or Excel workbook to run code, read files it should not, exhaust memory past the per-part
  size cap, or reach the XML parser with a document type declaration.
- Files written (`terraform sort` and `docs`, `logicapp export`, `xdr detections export`):
  any way to write outside the folder named, or through a link.
- Filter and path construction, where a name could alter an OData filter, a KQL query, an
  ARM path, an encoded query or the host a request goes to.
- The workflows in `.github/workflows/`, including their permissions and the release job.

Out of scope:

- The token checks not verifying signatures. This is documented behaviour: they answer "is
  this the token I meant to get?", not "is this token genuine?".
- What your own account is allowed to read. The tool uses your permissions and cannot grant
  more.
- Vulnerabilities in the Azure CLI or the services themselves. Report those to their
  owners, for Microsoft through the
  [Microsoft Security Response Center](https://msrc.microsoft.com/report).
- Vulnerabilities in dependencies that already carry a public advisory: govulncheck and
  Dependabot track those.

## Reporting a Vulnerability

Report privately using GitHub's private vulnerability reporting:

**https://github.com/libre-devops/ldo-go-cli/security/advisories/new**

Do **not** open a public issue for an undisclosed vulnerability, and never include a real
access token, tenant id or host name in a report. Use placeholders.

Please include the affected component and version or commit, reproduction steps, the impact
you believe it has, and any suggested remediation.

## What to Expect

- Acknowledgement of receipt within **3 business days**.
- An initial triage decision within **7 business days**.

If the report is accepted, we will develop a fix and coordinate disclosure timing with you
once a patch is released. If it is declined, we will tell you why. This is a
volunteer-maintained experiment, so please be reasonable about timelines.
