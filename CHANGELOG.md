# Changelog

Every change a user would notice goes under `## Unreleased`, which becomes the version's
section at a release. `ldo-go` follows the Python `ldo`: a change there that `ldo-go` takes
is noted here too.

## Unreleased

## 0.1.0

The first release: the Python `ldo` 0.8.1, ported to Go.

- Every command of the Python `ldo` 0.8.1: `devices`, `entra`, `intune`, `xdr` (incidents,
  detection rules and their export, the MDE Client Analyzer among them), `graph`, `azure`
  (Automation included), `keyvault`, `logs`, `pim`, `logicapp`, `snow`, `jira`,
  `confluence`, `news`, `planner`, `terraform`, `az`, `network test`, `json`, `profiles`,
  `config`, `welcome`, and the hidden `self-test`, with the same options, exit codes and
  output. Every command's JSON has the Python's keys and types.
- Signing in is pure Go, through azidentity and MSAL: `device-code` and `interactive`
  profiles need no Azure CLI, and without a `client_id` sign in as the Azure CLI's public
  client.
- The config file is the Python `ldo`'s own; sign-ins are kept in a file of `ldo-go`'s own.
- Releases carry a binary for Linux, macOS and Windows on amd64 and arm64, with
  `SHA256SUMS` and signed build provenance.

What differs from the Python `ldo`, and why, is in [docs/differences.md](https://github.com/libre-devops/ldo-go-cli/blob/main/docs/differences.md).
