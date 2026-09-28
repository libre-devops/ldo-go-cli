# Logic Apps

[Back to the docs](README.md)

Tooling for Consumption Logic App workflows, Sentinel playbooks among them, ported from the
LogicApps module in LibreDevOpsHelpers. Most of it reads files and never touches the network.

```bash
ldo-go logicapp check templates/                   # every .json and .json.tftpl in the folder
ldo-go logicapp check router.json --connection azuresentinel --supplied tenant_id --strict
ldo-go logicapp params export.json --unsatisfied   # parameters that will have no value
ldo-go logicapp references dist/ --unwired         # connection keys used but never wired
ldo-go logicapp connections export.json            # resolved connections, managed identity or not
ldo-go logicapp order dist/                        # the deploy order, from dispatch actions
ldo-go logicapp diff dist/router.json raw/router.json
ldo-go logicapp defaults export.json --out portable.json
ldo-go logicapp rewrite export.json --replace rg-old=rg-new --out lifted.json
ldo-go logicapp export -g rg-soc-uks-dev-01 --out raw/     # deployed workflows to files
ldo-go logicapp validate export.json -g rg-soc-uks-dev-01  # Azure's verdict; nothing is deployed
```

`check` (an error, or with `--strict` any warning), `references` (an unwired key), `diff` (a
difference) and `validate` (a rejection) exit 3, so each can gate a pipeline. `validate` asks the resource provider, which type-checks the whole definition and
creates nothing.

## What the checks know

- **Shapes.** A definition arrives as the designer's code view, an ARM resource, or a bare
  template definition. A wrapper's `parameters` holds values; a bare definition's holds
  declarations. Every command unwraps first, so `diff` compares a code view with an ARM export
  of the same workflow cleanly.
- **Templates.** `.json.tftpl` files are read with their `${...}` tokens blanked, so their
  shape is checked without rendering. `$${` is the escaped literal; `@{...}` expressions are
  left alone.
- **Parameters.** Every declared parameter needs a value by deploy time: a `defaultValue`,
  the wrapper, `--supplied` (what your deployment passes), or for `$connections` the
  deployment itself. A SecureString or SecureObject never comes from the wrapper.
- **Connections.** A definition names each connection by a key nothing checks until the
  workflow runs. `references` finds every key used, in triggers and nested actions, and says
  whether it is wired.
- **Order.** Azure checks a Workflow dispatch action's target when the caller is saved, so a
  caller deploys after what it calls. `order` works that out from the dispatch actions.
