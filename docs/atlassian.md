# Jira and Confluence

[Back to the docs](README.md)

`ldo-go jira` and `ldo-go confluence` read Atlassian Cloud as your own account, with an API token:
they see what you can in Jira and Confluence, and nothing more.

```bash
ldo-go jira whoami                                   # who the token reads as, and on which site
ldo-go jira issues --project OPS                     # a project's issues that are not done
ldo-go jira issues "assignee = currentUser() AND statusCategory != Done ORDER BY priority DESC"
ldo-go jira issue OPS-123                            # one issue, its description as Markdown
ldo-go jira issue OPS-123 --markdown > OPS-123.md    # the description alone
ldo-go jira projects                                 # the projects you can see
ldo-go confluence spaces                             # the spaces you can see
ldo-go confluence pages --space OPS                  # a space's pages, newest change first
ldo-go confluence page 123456                        # one page, its body as Markdown
ldo-go confluence page 123456 --markdown > runbook.md
ldo-go confluence search 'type = page AND text ~ "restart"'
```

## Signing in

Make an API token at [id.atlassian.com](https://id.atlassian.com/manage-profile/security/api-tokens),
then either set three variables:

```bash
export JIRA_INSTANCE="https://contoso.atlassian.net"   # or just contoso
export JIRA_EMAIL="you@example.com"                    # the account the token belongs to
read -rsp "API token: " JIRA_TOKEN && export JIRA_TOKEN
```

or add profiles to the config file (`ldo-go config init` writes a template), one for each
site, and pick one with `-p` (or `LDO_ATLASSIAN_PROFILE`):

```toml
[atlassian]
default_profile = "work"

[atlassian.profiles.work]
site = "https://contoso.atlassian.net"
email = "you@example.com"
# token_env = "JIRA_TOKEN"   the variable holding its token, for a second site
```

The token is never in the file, never on the command line, and never printed: it goes with
the email as HTTP Basic, Atlassian Cloud's way for API tokens. Jira and Confluence share it.

## Jira

`issues` takes a JQL query, or `--project KEY` for that project's issues that are not done.
Jira refuses a query with no restriction at all, so without either it is every issue not
done, newest change first; `-n` sets how many (50 by default). Each row has the key, type,
status (green when done, amber in progress), priority, assignee, when it changed and the
summary. `issue` shows one issue's details, then its description as Markdown, and
`--markdown` prints the description alone.

## Confluence

`pages` lists pages, the most recently changed first: every space's, or one's with
`--space` (a personal space's key is `~` and an id, as `spaces` shows it), or those with
one `--title`. `page` shows a page's details, then its body as Markdown: headings,
emphasis, links, lists, tables, quotes, code (Confluence's code macro among it) and tasks,
with macro settings left out. `search` takes CQL, Confluence's query language.
