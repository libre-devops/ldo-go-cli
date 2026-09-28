# Development

[Back to the docs](README.md)

You need Go (the version in `go.mod`, or later) and [just](https://github.com/casey/just).
Nothing else: the linters run at pinned versions through `go run`, and are never added to
`go.mod`.

```bash
just check                 # gofmt, go vet, the tests with coverage: what every change must pass
just ci                    # all of CI's checks: the above, staticcheck and govulncheck
just fmt                   # format every Go file
just test-one ./internal/core/network   # one package's tests, verbosely
just coverage              # what the tests leave uncovered, as a page to open
just record-json           # record the JSON shapes again, after a change that is meant
just run graph whoami      # build and run ldo-go from the working tree
just install               # put the working tree's ldo-go on PATH, in ~/go/bin
just dist 0.1.0            # the binaries for every platform a release carries, in dist/
just secrets               # the secret scan, uncommitted changes included (needs gitleaks)
```

Run `just` for the full list. See [CONTRIBUTING.md](../CONTRIBUTING.md) before opening a pull
request, and [AI.md](../AI.md) for the conventions, which hold for people and AI coding
assistants alike.

## Layout

```text
cmd/ldo-go/             main: signals, then cli.Execute
internal/core/          vendor-neutral: errors, config, brand, the HTTP client, the network
                        rules and probe, the token store, polling, inputs and workbooks,
                        row filters, logging, the YAML and JSON writers, Markdown, render,
                        colour, sorting, text files, the browser
internal/microsoft/     the shared Microsoft layer (clouds, profiles, tokens, API clients),
                        with identity/ (every sign-in) and azcli/ (the Azure CLI) as helpers
internal/microsoft/*/   one package per API: entra, xdr, graph, azure, pim, logicapps, ...
internal/servicenow/    the ServiceNow layer (config, OAuth, the Table API), with instance/
internal/atlassian/     the Atlassian layer (config, API tokens), with jira/ and confluence/
internal/terraform/     modules as files (the HCL splitter, the tools), with sort/ and docs/
internal/cli/           the command: parses, calls a client, renders
internal/fakes/         the test fakes, one package per concern
internal/project/       tests of the project itself: layering, docs, dashes
```

`internal/project/layering_test.go` holds the layering: `core` imports nothing of ours but
`core`, a feature package only `core` and its vendor's layer (a composite, `devices`, also
the features it combines), and only `cli` prints or exits. It also fails for a package with
no tests.

## Tests

Every package has its tests beside it. They never touch the network, a real `az` or a real
clock, so an hour-long watch runs in microseconds:

| Fake | Stands in for |
| --- | --- |
| `httpfake` | every HTTP call: a transport that answers from routes, and records each request |
| `azfake` | the Azure CLI: its accounts, and any command's answer |
| `clockfake` | the clock and sleeps, so a watch moves on at once |
| `tokenfake` | a token provider, with the claims a test gives |
| `graphfake`, `xdrfake`, `armfake` | Graph's, Defender's and Resource Manager's records |
| `snowfake` | a ServiceNow instance: the Table API, OAuth's grants and PKCE, and a person approving a sign-in |
| `atlassianfake` | a Jira and Confluence site, paged as the real ones page |
| `terraformfake` | a module on disk, and terraform fmt and terraform-docs |
| `certfake` | certificate authorities and the certificates they sign, for the TLS tests |
| `workbookfake` | Excel workbooks built by hand |

CLI tests run through the harness in `internal/cli/harness_test.go`: `newHarness(t,
handler)`, then `h.ok(args...)` for a command that must succeed, `h.fails(code, args...)`
for one that must not, `contains` to check output, and `usageError` for a usage message. It
runs in UTC, and each run starts afresh, as a new process would.

`internal/cli/json_output_test.go` runs every command with `-o json` and compares the shape
of what it writes (each key, and the kind of value under it) with the record in
`internal/cli/testdata/json-output.json`. Scripts read that JSON, so a key renamed or
dropped fails there. A change that is meant goes in the changelog, then into the record with
`just record-json`. A new command needs a case there.

The docs are tested too: every `ldo-go` example in the README, AI.md and `docs/` runs with
`--help`, every `just` example is a recipe, every relative link and heading it names is
there, and no file holds an em or en dash.

## Checking parity with the Python ldo

The Python `ldo` is the reference, and a difference is a bug unless
[How it differs](differences.md) says why. Where the Python has a golden output (KQL, YAML,
detection exports, analyzer reports, Message Center tasks, Logic App rewrites), the Go test
compares with it byte for byte, from `testdata/`. For the rest, run both side by side, with
the same config and inputs:

```bash
diff <(ldo-go xdr timeline web01 --since 1h --show-query) <(ldo xdr timeline web01 --since 1h --show-query)
diff <(ldo-go terraform sort ./module-a --check -o csv) <(ldo terraform sort ./module-b --check -o csv)
```

and compare the JSON shapes: `internal/cli/testdata/json-output.json` against the Python's
`tests/project/json_output.json`.

## Trying a build in a real tenant

The fakes cannot know everything a real tenant returns, so before a release, run the build
against one. `ldo-go self-test` (hidden from `--help`) runs every read-only command against
names you give, throws their output away, and reports what happened to each:

```bash
ldo-go self-test --device web01.corp.example --user ana@corp.example --group "Linux servers" --report self-test.json
ldo-go self-test --device web01.corp.example --only xdr --only devices
ldo-go self-test --device web01.corp.example --all
ldo-go self-test --workspace law-soc --vault kv-app --only logs --only keyvault --snow --atlassian
```

Each case that needs a name (`--device`, `--user`, `--group`, `--workspace`, `--vault`,
`--snow`, `--atlassian`) runs only when it is given, and a note counts the ones left out. It
reaches only what you name: give `--vault` a vault you can read.

| Result | Means |
| --- | --- |
| ok | it worked |
| attention | it exited 3, having found something, as designed |
| refused | it stopped with an error it explained: usually a permission or scope the sign-in lacks |
| usage | it rejected its arguments: a bug in the test or the command |
| CRASH | it panicked: a bug, with the lines of `ldo-go` it came through |

It exits 1 on a crash or a usage error. Nothing it runs changes anything, and it prints no
token; use a profile that is already signed in, since a sign-in cannot be answered while it
runs. Run the Python `ldo self-test` with the same names, and compare the two reports'
results: every command should end the same way in both. Rename anything from the tenant
before sharing a report.

## CI

| Workflow | What it does |
| --- | --- |
| `ci.yml` | gitleaks over every commit; `just ci` on Linux (gofmt, go vet, the tests with the coverage floor, staticcheck, govulncheck); the tests on macOS and Windows; the binaries for every platform, smoke tested and kept as the run's `dist` artifact |
| `release.yml` | on a `v*` tag: `ci.yml` as the gate, then the GitHub release of the binaries it built, with `SHA256SUMS` and signed build provenance |
| `codeql.yml` | CodeQL on the Go code and the workflows, on every change and weekly |
| `dependabot.yml` | weekly updates to the Go modules and the actions |

The third-party action (setup-just) is pinned to a commit, and gitleaks to a checksum.

On GitHub, turn on private vulnerability reporting (Settings, Security), Dependabot alerts,
and protect `main` so the `ci.yml` jobs must pass before a merge.

## Releasing

1. Turn `## Unreleased` in `CHANGELOG.md` into `## <version>` (a new, empty `## Unreleased`
   above it), merge to `main`, and let CI pass.
2. Run `ldo-go self-test` in a real tenant, from the `dist` artifact of that CI run or
   `just build`.
3. `just release <version>`: it checks the tree is clean and in step with `origin/main`, that
   the changelog has the version and the tag is new, then tags and pushes.

The release workflow runs CI again as its gate, checks the tag, the changelog and the
binaries' own `--version` agree, and only then creates the release: the six binaries (Linux,
macOS and Windows, for amd64 and arm64), `SHA256SUMS`, and the changelog's section as its
notes. The binaries carry signed build provenance, which anyone can check:

```bash
gh attestation verify ldo-go-linux-amd64 --repo libre-devops/ldo-go-cli
sha256sum --check --ignore-missing SHA256SUMS
```

A pre-release is the same with a version such as `0.2.0-rc.1`: it is marked as one, so it
never becomes the latest. The version comes from the tag, set into the binaries at build
time; `go install github.com/libre-devops/ldo-go-cli/cmd/ldo-go@v0.1.0` knows it from the
module instead. Never move or reuse a released tag.
