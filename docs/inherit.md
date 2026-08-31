# `inherit`: composing a guardfile from tiers

A guardfile can build on another. `inherit` names a base by relative path, and
the composition is resolved before anything is served. Spec resolution itself is
in [spec-mode.md](spec-mode.md).

## Composing tiers with `inherit`

```kdl
wrap ward mcp forgejo {
    inherit "operator/forgejo.kdl"
    auth header-token { header Authorization; prefix "token "; value env "FORGEJO_TOKEN" }
    never delete repo
    never create repo
}
```

Grants merge. `spec`, `base-url`, and `auth` are child-wins singletons, so a tier
states its own credential and inherits the policy. `restrict` dedupes by param.

A child can only **narrow**. Its `never` shadows an inherited `can`, and a bare
`can` that crosses an inherited `never` is a parse error naming
`override can <verb> <resource>` as the way to escalate on purpose. That is what
makes "this tier is weaker than its base" structural rather than a claim to
re-check by reading two files.

**A denied leaf mints no tool.** It is not a tool that refuses. Deny-by-absence
is the whole guarantee, so a `never` removes the operation from `tools/list` and
from `/api/` entirely.

## Siblings compose too

The nodes beside `wrap` travel the same chain. Most union, so a base tier's
`confirm` and `withhold` bind on every tier below it. `instructions`,
`server-info`, and `rate-limit` are child-wins. Rules and the reason:
[guardfile-siblings.md](guardfile-siblings.md).

## Narrowing needs spec mode

The inline grammar accepts only `can`. It has no `never`, so an inline guardfile
can inherit to **add** and cannot inherit to subtract. A tier that exists to be a
strict subset of its base has to be in spec mode.

## Flatten before you mount

A pod holds one mounted guardfile and cannot follow an `inherit` path, so the
composition is resolved at author time and the result is committed:

```sh
mcp-beaver flatten services/forgejo-mcp/forgejo.mcp.kdl -o services/forgejo-mcp/forgejo.flat.mcp.kdl
mcp-beaver flatten services/forgejo-mcp/forgejo.mcp.kdl -o services/forgejo-mcp/forgejo.flat.mcp.kdl --check
```

The flattened artifact is what the chart mounts and what a reviewer reads.
`--check` regenerates and compares without writing, failing on a stale committed
file. The output leads with a banner naming the source to edit instead.

Comments do not survive the flatten: KDL re-emission keeps data and drops prose.
Rationale belongs in `describe`, which does.

**One guardfile, not every file it names.** An `app` keeps its `file=` reference,
so the widget ships beside the artifact and reaches the pod through the chart's
`widgets` values rather than through that path. `--check` therefore covers the
composition and not the widgets: editing one leaves it green, correctly, since
the artifact regenerates byte-identical and failing would mean rewriting an
unchanged file. `just check-app-mount` covers the widget, in CI. See
[apps.md](apps.md).

## See also

- [guardfile-siblings.md](guardfile-siblings.md) - the sibling nodes and their
  per-node composition rules.
- [apps.md](apps.md) - `app`, whose widget ships beside the artifact.
- [chart.md](chart.md) - what a deployment mounts.
