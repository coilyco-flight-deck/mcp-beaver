# spec mode: resolving grants against an API document

A `.mcp.kdl` reaches its operations two ways now. Both end at the same
`opcore.Descriptor` and the same guarded `Execute`, so nothing downstream of the
parse changes.

- **inline grammar** - the frozen default. Each grant states its own `path`,
  `query`, and `body`. No spec file, no resolution.
- **spec mode** - the guardfile states `spec <file>` and each grant names a verb
  and a resource. umbra resolves those against the API document by convention.

`parseSource` picks between them: `inherit` composes first, then a `spec` node in
the result selects resolution. A guardfile with neither takes exactly the path it
took before, byte for byte. Composition is in [inherit.md](inherit.md).

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

## Deploying a spec-mode guardfile

The volume enumerates what it projects, and the API document is already in that
list, so it lands beside the guardfile with no chart change:

```sh
helm upgrade --install forgejo-mcp mcp-beaver \
  --set-file spec=forgejo.flat.mcp.kdl \
  --set-file apiDocument=forgejo.swagger.v1.json.gz \
  --set apiDocumentName=forgejo.swagger.v1.json.gz
```

`apiDocumentName` must be spelled exactly as the guardfile's `spec` node spells
it, because the runtime resolves the sibling by that name. It rides in
`binaryData`, since a gzipped document is not valid UTF-8. Supplying the
document without naming it fails the render. The chart parses neither file.

## Known gap

An inherited guardfile drops its parent's `action` nodes, so a leaf the parent
replaced comes back in generated form, and `lint` does not warn. See #108.

## See also

- [inherit.md](inherit.md) - composing tiers, and what `flatten` covers.
- [serve.md](serve.md) - the runtime and the grant-to-tool projection.
- [lint.md](lint.md) - validation, which covers both modes through `New`.
- [chart.md](chart.md) - the deployment surface.
