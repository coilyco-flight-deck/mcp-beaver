# spec mode and `inherit`: composing a guardfile

A `.mcp.kdl` reaches its operations two ways now. Both end at the same
`opcore.Descriptor` and the same guarded `Execute`, so nothing downstream of the
parse changes.

- **inline grammar** - the frozen default. Each grant states its own `path`,
  `query`, and `body`. No spec file, no resolution.
- **spec mode** - the guardfile states `spec <file>` and each grant names a verb
  and a resource. umbra resolves those against the API document by convention.

`parseSource` picks between them: `inherit` flattens first, then a `spec` node in
the result selects resolution. A guardfile with neither takes exactly the path it
took before, byte for byte.

## Why spec mode

The inline grammar makes every consumer restate the upstream API. Two guardfiles
against one service drift into two vocabularies for the same endpoints, and no
tool can tell you whether one surface is weaker than the other, because the sets
are not written in comparable terms. Resolving both against the same document
makes them diffable. See `coilyco-flight-deck/agentic-os#1365`.

```kdl
wrap ward mcp forgejo {
    spec forgejo.swagger.v1.json.gz
    base-url "https://forgejo.coilysiren.me/api/v1"
    auth header-token { header Authorization; prefix "token "; value env "FORGEJO_TOKEN" }

    can get repo          // resolves to GET /repos/{owner}/{repo}
    can list issue        // resolves to GET /repos/{owner}/{repo}/issues
}
```

The document is a **sibling** of the guardfile, named by file and never by path.
A `.gz` is decompressed on read, bounded at 16 MiB, so a vendored spec stays
small enough to travel in a ConfigMap.

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

## Narrowing needs spec mode

The inline grammar accepts only `can`. It has no `never`, so an inline guardfile
can inherit to **add** and cannot inherit to subtract. A tier that exists to be a
strict subset of its base has to be in spec mode.

## Flatten before you mount

`inherit` resolves against the filesystem by relative path. A pod holds one
mounted file and cannot follow one, so the composition is resolved at author time
and the result is committed:

```sh
mcp-beaver flatten services/forgejo-mcp/forgejo.mcp.kdl -o services/forgejo-mcp/forgejo.flat.mcp.kdl
mcp-beaver flatten services/forgejo-mcp/forgejo.mcp.kdl -o services/forgejo-mcp/forgejo.flat.mcp.kdl --check
```

The flattened artifact is what the chart mounts, so it is what a reviewer must
read. `--check` regenerates and compares without writing, and fails when the
committed file is stale, which is the CI half. The output leads with a generated
banner naming the source to edit instead.

Comments do not survive the flatten: KDL re-emission keeps data and drops prose.
Rationale belongs in the source guardfiles and in `describe`, which is data and
does survive.

## Deploying a spec-mode guardfile

The chart mounts the whole ConfigMap at `/spec`, so the API document lands beside
the guardfile with no volume change:

```sh
helm upgrade --install forgejo-mcp mcp-beaver \
  --set-file spec=forgejo.flat.mcp.kdl \
  --set-file apiDocument=forgejo.swagger.v1.json.gz \
  --set apiDocumentName=forgejo.swagger.v1.json.gz
```

`apiDocumentName` must be spelled exactly as the guardfile's `spec` node spells
it, because the runtime resolves the sibling by that name. It rides in
`binaryData`, since a gzipped document is not valid UTF-8. Supplying the document
without naming it fails the render rather than mounting something unreachable.
The chart still parses neither file.

## Known gap

An inherited guardfile drops its parent's `action` nodes, so a leaf the parent
replaced comes back in generated form. `lint` does not warn about that yet. See
issue #108.

## See also

- [serve.md](serve.md) - the runtime and the grant-to-tool projection.
- [lint.md](lint.md) - validation, which covers both modes through `New`.
- [chart.md](chart.md) - the deployment surface.
