# Pulling a guardfile from the registry

`mcp-beaver pull <registry-name>` writes a `wrap mcp upstream` guardfile for
one server in the official MCP registry: the entry's remote, the tool surface
it serves right now, and an allowlist decided by the upstream's own
`readOnlyHint`. The file is what `serve-upstream`, `lint`, and
`lint-upstream` then read. Spec and measurements: mcp-beaver#119.

```sh
mcp-beaver pull ac.tandem/docs-mcp > tandem.mcp.kdl
mcp-beaver pull ac.tandem/docs-mcp --scope read-write -o tandem.mcp.kdl
mcp-beaver pull my/private --upstream https://mcp.example/mcp \
    --upstream-header 'Authorization=Bearer {env:TOKEN}'
```

## What it does

1. `GET <registry>/v0/servers/<name>/versions/latest`, unauthenticated. The
   entry must be `active` and publish a `streamable-http` remote. A
   `packages`-only server would have to be installed and run, and nothing
   here does either. `--upstream <url>` skips the lookup and connects there.
2. Connect the way the proxy connects, `initialize` and `tools/list`, with
   one retry when the first list is empty: one registry server answered zero
   tools and then 25 on the same session shape, so a single reading produces
   false negatives.
3. Read each tool's `readOnlyHint` off the upstream's own JSON, beside the
   SDK session. The typed view folds an absent hint into false, and absent
   and false are different evidence.
4. Emit the file, parse it back, and only then write it.

It reads and never calls. The hint is self-declared, so an upstream that
mislabels is trusted, and nothing in this pipeline invokes a tool to check.

## Scope, an evidence axis

`--scope` selects tools by what the upstream asserted, and each step is an
ordered subset of the next, so widening adds lines without moving any.

- **`read-only`** - tools declaring `readOnlyHint: true`. The default, and the
  only scope to deploy unread.
- **`read-write`** - tools declaring the hint either way. Literally reads and
  writes, both asserted by the author.
- **`all`** - adds the tools that declare nothing, as their own deliberate
  step rather than riding along inside a word.

The allow rule is the hint and never the tool name. Measured on 120 tools
carrying a hint, name screening caught 6 to 10 of 24 real mutators, and the
misses included `fire_billing_event`, `agentra_authorize_payment`, and
`purchase_wizard` (mcp-beaver#118). The mutation lives in the noun, or nowhere
lexical at all.

## What the file carries

- **`annotation-coverage <declared|partial|undeclared> annotated=<n>
  silent=<n>`** - whether the upstream annotates every tool, some, or none.
  It decides whether a guardfile can safely allow anything, no directory
  publishes it, and a consumer filters on it rather than parsing comments.
  Measured across 30 servers: 15 declare every tool, 2 some, 13 none.
- **`instructions`** - the registry description, folded onto one line and
  cut inside the 500-character budget, as the sibling every guardfile uses.
- **`auth header-token`** - the `--upstream-header` the pull presented, when
  it has the `<prefix>{provider:address}` shape the node can state.
- **No schemas.** The proxy snapshots the upstream contracts at connect time
  and fails closed on drift, so restating them in KDL would duplicate a
  source of truth that rots. The file carries policy.

## What it refuses to write

- **No `withhold` block per excluded tool.** `withhold` mints a discoverable
  stub that appears in `tools/list`, so one per exclusion advertised 208
  tools of which 96 answered, and a consumer pays for that list every turn.
  The allowlist is closed by construction, a denial list enforces nothing,
  and it only goes stale. `withhold` stays a hand-authoring device.
- **No guessing.** A tool declaring nothing is absent under `read-only` and
  `read-write`. For 13 of 30 registry servers that is a guardfile allowing
  nothing, and that is the correct output: `lint` accepts it, and
  `serve-upstream` refuses to serve it, because exposing nothing is a
  statement rather than a server.

## See also

- [upstream.md](upstream.md) - the proxy the file configures, and the
  guardfile grammar.
- [registry-pull.md](registry-pull.md) - the Python prototype and the
  30-guardfile corpus this was measured on.
- [directory.md](directory.md) - `directory`, the same pull run across the
  registry, with the pages that index the result.
