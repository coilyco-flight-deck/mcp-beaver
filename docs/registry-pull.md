# The registry-pull prototype

`scripts/registry-probe/` is the working prototype behind
[mcp-beaver#119](https://forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver/issues/119),
the spec for a `wrap mcp upstream` guardfile node and a `mcp-beaver pull`
command. It is Python beside the Go runtime on purpose. Nothing here ships in
the image, the chart, or the binary, and beaver cannot parse what it emits
today. It exists so the spec has a measured origin and a rebuildable dataset,
rather than a description of scripts that died with a session.

## What it does

- **`probe.py`** - enumerates `registry.modelcontextprotocol.io/v0/servers`,
  keeps `isLatest` and `status: active`, takes the first `remotes[].url`, runs
  `initialize`, `notifications/initialized`, and `tools/list` against each, and
  writes `probe.json`. Replies are SSE-framed, so it strips `data:` lines. It
  retries an empty `tools/list` once, because one server answered zero tools
  and then 25 on the same session shape.
- **`classify.py`** - sorts stored tools into reads, mutates, and unknown from
  the tool name, offline. This is the heuristic
  [mcp-beaver#118](https://forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver/issues/118)
  measured at 6 to 10 of 24 real mutators caught. It feeds the directory page
  and nothing else. Generation never uses it.
- **`gen_kdl.py`** - writes one guardfile per answering server into
  `guardfiles/`, allowing only tools whose upstream declares
  `readOnlyHint: true`, and prints the `serve-upstream` invocation that runs
  the same allowlist with no new grammar.
- **`render.py`**, **`render_all.py`**, **`render_interactive.py`** - render
  the directory page, the guardfile dump, and the three-scope interactive page
  from `probe.json` and the `shell*.html` templates into `out/`.

## Running it

- `just registry-guardfiles` regenerates and `kdlfmt`-formats the guardfiles
  from the committed probe. Offline, and the committed files are its output.
- `just registry-render` renders the three pages into
  `scripts/registry-probe/out/`, which is ignored.
- `just registry-probe` reaches the live registry and rewrites `probe.json`.
  Every number below moves when it runs.

## The 2026-09-01 run

One run, one host, one day. 60 entries enumerated, 30 answered anonymously,
30 refused: 21 with HTTP 401, three with 503, three with no result, one 402,
one connection failure, one "Missing authentication token" inside MCP.

208 tools across the 30. 96 declare `readOnlyHint: true`, 24 declare it
false, 88 declare nothing. By server: 15 declare every tool, 2 declare some,
13 declare nothing. The 13 correctly yield a guardfile that allows nothing and
says so. All 30 files parse under `kdlfmt`.

## The rules generation follows

- **Allow on `readOnlyHint`, never on the name.** Name screening missed
  `fire_billing_event`, `agentra_authorize_payment`, and `purchase_wizard`.
  The mutation lives in the noun, or nowhere lexical at all.
- **No `withhold` blocks.** `withhold` mints a discoverable stub that appears
  in `tools/list`, so one per excluded tool would advertise 208 tools of which
  96 answer, and a consumer pays for that list every turn. The allowlist is
  closed by construction, so a denial list enforces nothing and only goes
  stale. `withhold` stays a hand-authoring device. Dropping the blocks took the
  30 files from 34,118 to 24,061 bytes.
- **No guessing.** An undeclared tool is excluded, and the file carries
  `annotation-coverage <declared|partial|undeclared> annotated=<n> silent=<n>`
  so a consumer filters on it rather than parsing comments.
- **Upstream order, ordered subsets.** Tool order follows the upstream's own
  `tools/list`, so read-only is an ordered subset of read-write, which is an
  ordered subset of everything. Widening adds lines without moving any.

## What it is not

The `wrap mcp upstream` node and `annotation-coverage` marker are proposed
grammar. They are valid KDL and not a valid guardfile, so the generated files
live here rather than under `examples/`, where `just lint-examples` would
reject them. `readOnlyHint` is self-declared, and nothing in this pipeline
calls a tool to check it. Local `packages` upstreams over stdio, scheduling,
and history are out of scope, as the spec says.

## See also

- [upstream.md](upstream.md) - `serve-upstream`, the runtime this generates
  for, and `lint-upstream --read-only`.
- [DESIGN.md](DESIGN.md) - why the file carries policy and never schemas.
