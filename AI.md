# Instructions for AI coding assistants

This file is for Claude Code and any other assistant working in this repository. `CLAUDE.md`
imports it.

## What this is

`ldo-go`: the Go port of the Python `ldo` (github.com/libre-devops/python-helpers), a fast,
read-only CLI for Microsoft (Entra ID, Defender XDR, Intune, Azure, Graph, PIM, Logic Apps,
Automation), ServiceNow and Atlassian (Jira, Confluence), with helpers for Terraform modules.
Its users are people signing in as themselves; automation is second. It is an experiment in
how far a port gets: the Python `ldo` is the reference, and a difference from it is a bug
unless `docs/differences.md` says why.

Everything reads, apart from `ldo-go az use`, which switches the Azure CLI's account,
`planner add-news --write` and `add-rollup --write`, which raise and update Planner tasks,
`terraform sort` and `docs`, which change a module's own files (`--check` only reads), and
`logicapp export`, which writes files. Never add a command that changes a tenant or an
instance without being asked.

## Working here

- Go 1.27 or later, at `~/.local/go/bin` on this machine; the justfile puts it on PATH.
- Run everything through `just`: `just check` before calling anything done (gofmt, go vet,
  the tests with a coverage floor of 85%), `just ci` for staticcheck and govulncheck too,
  `just fmt` to format, `just run <args>` to try the CLI, `just record-json` after a meant
  change to a command's JSON.
- Dependencies: cobra, BurntSushi/toml, azidentity and azcore, MSAL for Go, x/term, x/net, x/sys and
  pkg/browser. Do not add one without asking; everything else is the standard library.

## Layout and layering

```text
cmd/ldo-go/             main: signals, then cli.Execute
internal/core/          vendor-neutral: errs, config, brand, httpx, network and probe,
                        tokenstore, poll, inputs and sheets (CSV and Excel), rowfilters,
                        logging, yamltext and jsontext (Python's writers, byte for byte),
                        markdown, render, colour, sorting, textfiles, webbrowser
internal/microsoft/     the shared Microsoft layer: clouds, profiles, tokens, API clients;
                        identity and azcli are its helpers
internal/microsoft/*/   one package per API: entra, xdr, graph, azure, pim, ...
internal/microsoft/devices/  the one composite, using entra, xdr and intune
internal/servicenow/    the ServiceNow vendor, with instance/ on it
internal/atlassian/     the Atlassian vendor, with jira/ and confluence/ on it
internal/terraform/     modules as files: the HCL splitter and the tools, with sort/ and docs/
internal/cli/           the ldo-go command: parses, calls a client, renders. No logic here.
internal/fakes/         test fakes, one package per concern
```

`internal/project/layering_test.go` enforces it: `core` imports nothing of ours but `core`, a
feature imports only `core` and its vendor layer, `cli` sits on top, and fakes are for tests.
It also fails for a package with no tests. A new vendor goes in its `vendors` map.

## Tests

- Every package has tests beside it. Nothing touches the network, a real `az` or a real
  clock: HTTP goes through `fakes/httpfake`, `az` through `fakes/azfake`, time through
  `fakes/clockfake`, and each service has a fake of its own (`graphfake`, `snowfake`,
  `atlassianfake`, `terraformfake`, ...). A loopback server is fine where a real handshake is
  the point (`core/probe`).
- CLI tests use the harness in `internal/cli/harness_test.go`: `newHarness`, then `h.ok`,
  `h.fails(code, ...)`, `contains`, and `usageError` for a usage message. It runs in UTC.
- `internal/cli/json_output_test.go` records every command's JSON shape in
  `testdata/json-output.json`; a change that is meant is recorded with `just record-json`.
  A new command gets a case there.
- Where the Python gives a golden output (KQL, YAML, detections, analyzer reports), the Go
  test compares with it byte for byte, from `testdata/`.

## Names and branding

Never hard-code `ldo-go`, `LDO_` or the repository: use `internal/core/brand` (`brand.Command`,
`brand.EnvVar("X")`, `brand.Suggest("config init")`, `brand.Docs("page")`). The config file is
the Python `ldo`'s own (`brand.ConfigDir`); sign-ins are kept in a file of this tool's own.

## Writing code

- Write for the next person to read it: small functions, split into named steps; a doc
  comment on everything exported saying what it gives; a comment says why, where the code
  cannot. Prefer a plain loop to a clever expression. Match the surrounding code.
- A model reads its JSON through `core/fields` (`Text`, `Map`, `Objects`, `Strings`, `When`,
  `Number`); an id that goes into a path through `util.RequireGUID`. Anything a person types
  that goes into a URL path or a query is validated first, with an anchored pattern.
- Return the specific error from `core/errs`: `Inputf` for something given that cannot be
  used, `Configf` for a profile that cannot do this, `NotFoundf`, `Ambiguousf`, `Authf`,
  `Reauthf`, `APIf`, with `WithHint` saying what to do. Library code never exits or prints;
  only the CLI turns errors into messages and exit codes (0 fine, 1 error, 2 usage, 3 needs
  attention, 130 interrupted).
- Data goes to stdout; notes, warnings and progress to stderr, through `render.Console`
  (`Note`, `Warn`, `Emit`). Every data command takes `-o` and `-p` through `Common`; a list
  also takes `--sort` and `--unique`.
- All HTTP goes through `core/httpx`, which follows `core/network` (the proxy, `no_proxy`
  and the certificates); `az` runs with the same environment. Never build an `http.Client`
  of your own.
- A token or secret is never printed (only with an explicit `--raw`), never logged, and never
  accepted on the command line. A type holding one has a `String` and `GoString` that leave
  it out. A proxy address can hold a password: show one only through `network.Redact`.
- Go maps lose order: where a file is written back, or order is part of the output, use
  `yamltext.Ordered`.
- Python counts characters, Go bytes: a limit that Python applies to a string's length is a
  rune count here.

## Docs

- `README.md` is a short front page: the command table, install, quickstart and links.
  Detail goes in `docs/`, one page per area, indexed in `docs/README.md`; `docs/development.md`
  covers building, the tests, CI and releasing. `CHANGELOG.md` gets an `Unreleased` entry
  for every change a user would notice.
- The command pages follow the Python `ldo`'s, since the commands are the same: a change to a
  command there, taken here, changes its page here too. `docs/differences.md` lists every way
  the two differ, and gets a line for each new one.
- `internal/cli/docs_test.go` runs every `ldo-go` example in the docs with `--help`, and
  `internal/project/docs_test.go` checks every `just` example is a recipe, every relative
  link and heading it names is there, and no file holds an em or en dash. Keep examples real.
- UK English (colour, organisation, licence as a noun). Never use em or en dashes, in any
  file: use commas, colons, brackets or a plain hyphen.

## Commits and releases

- Do not commit, push, tag or release until the person asks. Use their own git identity.
- CI (`.github/workflows/ci.yml`) runs gitleaks, `just ci` on Linux, the tests on macOS and
  Windows, and builds the binaries. A release is `just release <version>` once `CHANGELOG.md`
  has the version's section: `release.yml` then runs CI as its gate and publishes the
  binaries it built, with checksums and build provenance. Never move or reuse a released
  tag.
- No AI attribution in commits or pull requests: no `Co-Authored-By` or "Generated with"
  lines.
- Never put a real tenant's names or ids in a test: use `web01`, `corp.example` and the fakes'
  own ids.
