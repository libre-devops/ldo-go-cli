# Contributing to ldo-go

Contributions are welcome, whether that is reporting an issue, proposing a fix, or improving
the documentation. `ldo-go` is the Go port of the Python `ldo`, which is the reference: a new
command belongs there first, and a difference between the two is a bug unless
[docs/differences.md](docs/differences.md) says why.

## Workflow

Working with an AI coding assistant? Its instructions are in [AI.md](AI.md), which
`CLAUDE.md` imports. They are the conventions for people too.

1. Fork the repository and branch from `main`.
2. Install Go (the version in `go.mod`, or later) and [just](https://github.com/casey/just).
3. Make your change, with tests.
4. Run `just check`: gofmt, go vet, and the tests with coverage at or above the floor in the
   justfile. `just ci` runs staticcheck and govulncheck too.
5. Open a pull request using the template. CI runs `just ci` on Linux, and the tests on
   macOS and Windows; all of it must pass to merge.

Security issues are the exception: report those privately, as described in
[SECURITY.md](SECURITY.md).

## Code standards

1. Keep the layering, which `internal/project/layering_test.go` enforces:
   - `internal/core` is vendor-neutral, and depends on nothing else of ours. Put a shared
     helper here rather than copying it into a second package.
   - Each vendor (`internal/microsoft`, `internal/servicenow`, `internal/atlassian`,
     `internal/terraform`) has a shared layer for what all of its features need, and each
     feature package under it depends on `core` and that layer only. A composite
     (`internal/microsoft/devices`) may also use the features it combines.
   - `internal/cli` sits on top of everything, and nothing imports it. It parses, calls a
     client, and renders: no logic.
2. Library packages return `errs` errors with a hint saying what to do, and never exit or
   print. Only `internal/cli` turns errors into messages and exit codes.
3. Everything reads. A new command that writes to a tenant or an instance needs discussion
   in an issue first. Secrets come from the environment, never the config file or the
   command line.
4. Never log or print an access token. `auth.AccessToken`, and every credential type, keep
   the secret out of `String` and `GoString`; keep it that way.
5. Data goes to stdout, and notes, warnings and errors go to stderr, through
   `render.Console`, so output can be piped.
6. Keep the dependencies to those in `go.mod`. Anything else needs a good reason.
7. Everything must be testable, and tests must not touch the network, a real `az` or a real
   clock. Inject the HTTP client, the runner, the environment and the clock, and use the
   fakes in `internal/fakes`: `httpfake`, `azfake`, `clockfake`, and one per service.
8. Never hard-code the tool's names: they come from `internal/core/brand`.
9. Use UK English and plain ASCII in code, comments and docs: no smart quotes, em or en
   dashes, or ellipsis glyphs. A test checks for the dashes.
10. Never include real tokens, tenant ids, subscription ids or host names in code, tests or
    issues. Use the placeholder GUIDs and `corp.example` names the tests already use.
11. Keep coverage up, and never lower the floor to get a change in.

When you add or change a command, option or exit code, document it on its page in `docs/`,
record its JSON shape (`just record-json`), and add a line to `CHANGELOG.md`, in the same
pull request. A test runs every `ldo-go` example in the docs with `--help` and follows every
link, so a renamed command or option shows up there.

## Licence

By contributing, you agree that your contributions are licensed under the
[MIT Licence](LICENSE) that covers the project.
