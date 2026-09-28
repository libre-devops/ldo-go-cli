# How it differs from the Python ldo

[Back to the docs](README.md)

`ldo-go` was checked against the Python `ldo` side by side: the same commands against the same
tenant, instance and files, and the same fixtures in the tests. Tables, CSV, KQL, YAML, the
`json` command, Terraform files and READMEs, rewritten Logic App definitions and Message
Center tasks come out byte for byte the same; JSON records hold the same keys and values; and
`self-test` gives every command the same result in both. What differs is below.

## Signing in

- **Pure Go.** Every sign-in is Microsoft's own Go libraries: azidentity for the Azure CLI,
  client secrets, workload identity and managed identity, and MSAL for a person's device code
  or browser sign-in. A `device-code` or `interactive` profile needs no Azure CLI at all.
- **No app of your own needed.** Without a `client_id`, a `device-code` or `interactive`
  profile signs in as the Azure CLI's public client, with the Azure CLI's scopes. The Python
  `ldo` needs a `client_id` for either.
- **No offer to sign in again mid-command.** When a sign-in lapses, the error names the cause
  and what to run; the Python `ldo` offers, on a terminal, to sign the Azure CLI back in and
  carry on (`LDO_REAUTH`). `ldo-go` has no such prompt.
- **No keychain.** `token_cache = "keychain"` is taken, and the sign-in kept in the file.
- **Its own sign-ins.** They are kept in `ldo-go-sign-ins.json`, beside the Python `ldo`'s
  file rather than in it, so neither tool reads the other's refresh tokens.

## JSON output

- **Key order.** A record's keys come out sorted, where the Python `ldo` keeps the order the
  service sent, or its own. The keys, their types and their values are the same, and
  `internal/cli/testdata/json-output.json` records every command's shape as the Python's
  `json_output.json` does. Where a file is written back (a Logic App export), the order is
  kept.
- **Times.** A timestamp ends in `Z` (`2026-09-24T12:00:00Z`), where Python's `isoformat()`
  writes `+00:00`. Both are RFC 3339 in UTC.

## The network

- **Certificates.** Go verifies against the machine's own store (where IT installs a
  TLS-inspecting proxy's root), with `ca_bundle` added, and has no public roots of its own,
  as the Python `ldo` has from certifi. `network test` says so: its `Certificates` line
  reads "this machine's store", and its JSON has `system_store` and `extra` (the
  certificates `ca_bundle` adds) in place of the Python's `public`, `system` and `extra`
  counts. The Azure CLI, which trusts one file alone, is still handed a bundle of the
  machine's certificates and `ca_bundle`'s, as the Python hands it.
- **The operating system's proxy setting** (Windows and macOS) is not read: name the proxy
  with `LDO_PROXY_ADDRESS`, `proxy` or `HTTPS_PROXY`.
- **A certificate that does not verify.** `network test` names its issuer from the
  certificate Go hands back with the error, rather than connecting a second time as the
  Python does, and says so when a server answers `https` in plain HTTP.

## Logs

OTLP records carry the message, its level, the attributes and the trace context as the
Python's do, but not the Python's `code.function.name`, `code.line.number` or `exception.*`
attributes.

## Smaller things

- **Help.** `--help` is cobra's, not Typer's panels; the text is the same.
- **The `json` command's errors.** A document that is not JSON is reported with Go's own
  words for why, at the same line and column.
- **Case.** Sorting and matching fold case the way Go's `strings.ToLower` does, which agrees
  with Python's `casefold` for everything but a few letters such as `ß`.
- **Line ends.** A Terraform file is split on `\n` (and so `\r\n`); Python's `splitlines`
  also splits on rarer line breaks such as a form feed.

## What it does not have

- **No library.** The Python `ldo` can be imported; `ldo-go`'s packages are `internal`, so
  it is a command and nothing else.
- **No container images or packages.** A release carries a binary for each platform, which
  needs nothing installed beside it; `go install` works too.
- **No rebrand.** The Python's `just rebrand` renames the whole project. Here every name
  lives in `internal/core/brand`, so a copy under another name changes that file, but no
  recipe does it for you.
- **No GitLab mirror.** The Python's repository is mirrored to GitLab and has a GitLab CI of
  its own; this one has GitHub's workflows only.
