# The guarded passthrough proxy

`mcp-beaver serve-upstream --upstream <mcp-url> --tool <name>...` connects to a
private streamable-HTTP MCP upstream, snapshots the allowlisted upstream tool
contracts, and exposes only that subset on the outward MCP and HTTP surfaces.

- **Projection** - each allowlisted upstream tool becomes one outward MCP tool
  and one matching HTTP endpoint, preserving the upstream schema and metadata
  where possible. Passthrough preserves upstream titles, annotations, and
  results without reclassifying them.
- **Fail closed** - unknown upstream tools and schema drift return MCP tool
  errors instead of silently widening or mutating the surface.
- **One session** - allowed calls forward to a single long-lived upstream MCP
  session, including the drift check, because a second session is what real
  Node MCP upstreams reject.
- **Session survival** - the upstream bound is time-to-first-byte, never a
  whole-exchange `Client.Timeout`: a streamable-HTTP response is a body that
  stays open, so a whole-exchange bound killed any long call and took the
  session with it. A session the upstream has forgotten is replaced on the next
  call. The failing call is not replayed, since it may already have reached the
  upstream, and the reconnect never re-snapshots the baseline, so drift still
  fails closed.
- **Bounded startup retry** - optional `--connect-timeout` retries the initial
  connection while a co-located upstream starts. Zero retains fail-fast.

**mcp-beaver lint-upstream --tool <name>....** The validation surface for an allowlist. Shape validation calls the same
`ValidateAllowlist` the serving path calls, so the check cannot drift from what
`serve-upstream` accepts. Empty entries, duplicates, and an empty list fail.
Offline by default, so it runs in CI and a sealed clone. A clean allowlist
exits 0 and prints the names sorted, one per line.

- `--read-only heuristic` screens names offline for mutation verbs and fails
  naming any suspect. A naming heuristic, not upstream truth, but it lives in
  the owning loader instead of being restated per consumer.
- `--read-only strict` requires `--upstream`, connects, and fails any tool the
  upstream does not annotate `readOnlyHint: true`. Authoritative, so it belongs
  in a rollout or smoke path. An unannotated tool counts as mutable.
- Passing `--upstream` builds the same proxy `serve-upstream` builds, so a tool
  absent upstream fails here too.
