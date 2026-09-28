# Terraform modules

[Back to the docs](README.md)

`ldo-go terraform` keeps a Terraform (or OpenTofu) module's files tidy the way its README
expects: its variables and outputs in name order, and a README made of a hand-written
`HEADER.md` above the tables terraform-docs writes. They are the only `ldo-go` commands that
change files, and only the files of the folders named; neither signs in anywhere, plans or
applies.

```bash
ldo-go terraform sort                        # this folder's variables.tf and outputs.tf, then terraform fmt
ldo-go terraform sort --inputs               # only variables.tf
ldo-go terraform sort --outputs              # only outputs.tf
ldo-go terraform sort -r                     # and every folder beneath: examples/, modules/
ldo-go terraform sort network.tf             # the variables and outputs in any .tf file
ldo-go terraform sort -r --check             # change nothing; exit 3 when a file is out of order
ldo-go terraform docs                        # README.md from HEADER.md and terraform-docs
ldo-go terraform docs -r                     # and each folder beneath with a HEADER.md of its own
ldo-go terraform docs -r --check             # change nothing; exit 3 when a README is out of date
```

A module's usual upkeep, as a `just` recipe in the module's own repository:

```text
docs:
    ldo-go terraform sort -r
    ldo-go terraform docs -r
```

## Sorting variables and outputs

`terraform sort` puts the `variable` blocks in each folder's `variables.tf` and the `output`
blocks in its `outputs.tf` in name order, ignoring case, which is the order terraform-docs
lists them in a README. Given a `.tf` file instead of a folder, it sorts that file's
variables and outputs. `--inputs` or `--outputs` sorts only the one.

Only the blocks move. A comment directly above a block (with no blank line between) moves
with it; everything else in the file, the blank lines between blocks included, stays where
it was. It reads the file the way Terraform does, as far as blocks go, so a `}` in a
heredoc description, a string, a `${ }` template or a comment never ends a block early,
and one-line blocks (`variable "tags" {}`) sort like any other. A file whose braces do not
balance is refused, as Terraform would refuse it, and left alone.

It then runs `terraform fmt` on what it sorted (`fmt -recursive` with `-r`), or OpenTofu's
`tofu fmt` when `terraform` is not on `PATH`. With neither, the files are still sorted, and a
warning says `fmt` was not run; `--no-fmt` skips it. Hidden folders such as `.terraform`
(the modules `terraform init` downloaded) are never looked in.

`--check` changes nothing: it lists each file, and exits 3 when any is out of order, for a
pipeline.

## The README

`terraform docs` writes each module's `README.md` in two parts:

```text
[HEADER.md: the title, what the module is for, a usage example, written by hand]

<!-- BEGIN_TF_DOCS -->
[the requirements, inputs and outputs tables, written by terraform-docs]
<!-- END_TF_DOCS -->
```

It puts `HEADER.md` at the top of the README, keeping the section between the markers (and
anything after it), then runs [terraform-docs](https://terraform-docs.io) to write that
section. terraform-docs is not part of `ldo-go`: install it and have it on `PATH`, and `ldo-go`
says how when it is not. It reads the module's own `.terraform-docs.yml` when there is one,
and otherwise writes a Markdown table. A folder without a `HEADER.md` keeps its README's own
top, and a README without the markers has them added at the end.

With `-r`, every folder beneath with a `HEADER.md` of its own (an example, a submodule) is
written too. `--header` and `--readme` name other files, found in each folder. `--check`
changes nothing, and exits 3 when a README is out of date: when its top is not `HEADER.md`,
or terraform-docs would write its tables differently.
