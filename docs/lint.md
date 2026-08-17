# Validating a guardfile without serving it

Split out of the README.

## Validating a guardfile without serving it

`mcp-beaver lint` is the offline check. It reads the spec, builds the same server
`serve` would, prints the minted tool names to stdout one per line, and exits -
no listener, no telemetry, no network, so it runs in a sealed clone and in CI:

```sh
$ mcp-beaver lint examples/skillsmp.mcp.kdl
search_ai-skills
search_skills
```

A spec that does not parse, or that projects two grants onto one tool name,
exits non-zero with the failure on stderr and prints nothing to stdout.

Two things warn rather than fail, because both are legitimate choices an author
has to be the one making: a verb that resolved to POST by fallthrough, and a
`resource` that states no `audience`. Neither is visible from any other
surface, so a spec carrying one reads exactly like a working spec. Warnings go
to stderr, so adding one never edits the list a consumer diffs.

The tool-name output matters as much as the exit code. A consumer repo diffing
that list against a reviewed expectation is invoking the owning loader. A
consumer reimplementing the parse to derive the same list is the antipattern
this command exists to remove. Linting goes through the full server build
rather than the raw KDL parse on purpose, so the check covers the
grant-to-tool projection the runtime will actually mint, not only what the file
says. `serve-ssm` policies use a separate grammar and are not lintable through
this path.

The forgejo example serves `create_issue`, `get_issue`, `list_issue`, `comment_issue`, `close_issue` - each guarded, each scoped to `coilyco-*` / `kai` owners.

## See also

- [authoring-siblings.md](authoring-siblings.md) - guardfile siblings.
- [FEATURES.md](FEATURES.md) - what ships today.
