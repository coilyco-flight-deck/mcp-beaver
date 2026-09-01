# Generating the directory

`mcp-beaver directory -o <dir>` sweeps the official MCP registry and writes a
directory of what its servers actually serve: a record of the sweep, one
`wrap mcp upstream` guardfile per answering server, and two pages that index
them. It is `pull` run across the registry, plus the pages. Spec:
mcp-beaver#121, following the prototype behind mcp-beaver#119.

```sh
mcp-beaver directory -o out                          # the whole registry, read-only
mcp-beaver directory -o out --limit 40 --scope all   # the first 40 listed, every declared tool
mcp-beaver directory -o out2 --from out/sweep.json   # re-render offline from an earlier sweep
```

## What it writes

- **`sweep.json`** - every server the registry listed, answered or not, with
  its state, description, published date, repository, and each tool's name,
  description, and `readOnlyHint` exactly as declared. An absent hint is
  absent in the file too, never folded into false. This is the only input the
  rest needs, so `--from` renders it again without the network.
- **`guardfiles/<name>.mcp.kdl`** - one per answering server, `/` in the
  registry name written as `__`, through the same renderer `pull` uses and
  parsed back before it is written. The rules are `pull`'s and unchanged:
  allow on the declared hint never the name, no `withhold` blocks, no
  guessing, upstream order.
- **`index.html`** - the directory. Counts for the run, one row per answering
  server with its tools split into declared read-only, declared mutating,
  and undeclared, its `annotation-coverage`, and a link to its guardfile.
  Then the refusals with their state.
- **`guardfiles.html`** - every guardfile, grouped by coverage.

## What it does

1. `GET <registry>/v0/servers?limit=100`, following `nextCursor`. Keep
   `isLatest` and `status: active` entries that publish a `streamable-http`
   remote, first remote wins, one entry per name. `--limit` caps the count in
   registry order.
2. Probe each entry the way `pull` does, `--concurrency` at a time and
   `--timeout` per upstream, 30s by default. A refusal is that server's state,
   `HTTP 401` when the upstream answered a status, `timeout` when it ran past
   the deadline, and the transport's own words otherwise, and never the
   sweep's failure. Half the registry refuses anonymously, and a directory
   that stopped at the first 401 would never be written. One line per server
   goes to stderr as it settles, so a long sweep is visibly moving.
3. Record, emit, and render from the record.

No credential is presented. The refused half stays outside the corpus, which
is the measurement the spec recorded, and `pull --upstream-header` remains
the way to reach one of them by hand.

## The counts are the hint, not a name

The index counts on `readOnlyHint` alone. The prototype's page classified
tools by their names, and mcp-beaver#118 measured that screen at 6 of 24 real
mutators, with payment tools among the misses. A directory column that
disagrees with the guardfile beside it is the one thing this page must not
do, so the heuristic feeds nothing here. A tool declaring nothing is counted
as undeclared rather than guessed at.

## See also

- [pull.md](pull.md) - one server, the same renderer, and the scope axis.
- [registry-pull.md](registry-pull.md) - the Python prototype this replaces
  the index and guardfile pages of, and the 2026-09-01 corpus.
- [upstream.md](upstream.md) - the proxy each generated file configures.
