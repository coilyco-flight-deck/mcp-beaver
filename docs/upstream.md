# The guarded passthrough proxy

`mcp-beaver serve-upstream --upstream <mcp-url> --tool <name>...` connects to a
streamable-HTTP MCP upstream, snapshots the allowlisted upstream tool contracts,
and exposes only that subset on the outward MCP and HTTP surfaces.

- **Projection** - each allowlisted upstream tool becomes one outward MCP tool
  and one matching HTTP endpoint, preserving the upstream schema, title,
  annotations, and results rather than reclassifying them.
- **Fail closed** - unknown upstream tools and schema drift return MCP tool
  errors instead of silently widening or mutating the surface.
- **One session** - allowed calls forward to a single long-lived upstream MCP
  session, drift check included, because real Node upstreams reject a second.
- **The upstream session is its own** - its standalone stream stays open, since
  an upstream may answer a `tools/call` there (#80), and a caller's values never
  cross: the SDK prefers their context protocol version over the session's, so a
  caller newer than the upstream made every request carry one it rejects (#85).
- **Session survival** - the upstream bound is time-to-first-byte, never a
  whole-exchange `Client.Timeout`: a streamable-HTTP response stays open and a
  whole-exchange bound took the session with it. A forgotten session is replaced
  next call, the failing one never replayed, the baseline never re-snapshotted.
- **Bounded startup retry** - `--connect-timeout` retries the initial connection
  while a co-located upstream starts. Zero retains fail-fast.
- **Upstream credentials** - `--upstream-header 'Authorization=Bearer
  {env:TOKEN}'` presents a header per request, which a hosted third-party MCP
  needs and a loopback sidecar never did. A `{provider:address}` span resolves
  through umbra's registry, the rest is literal, and one span is required since
  a span-free template puts the value in argv. It resolves per request like
  spec-mode `auth`, once more at startup to fail fast, and never into an error.

**mcp-beaver lint-upstream --tool <name>....** The validation surface for an allowlist, calling the serving path's own
`ValidateAllowlist` so the check cannot drift. Empty entries, duplicates, and an
empty list fail. Offline by default, so it runs in CI and a sealed clone, and a
clean run exits 0 and prints the names sorted. `--read-only heuristic` screens names for
mutation verbs offline, a heuristic living in the owning loader rather than
restated per consumer. `--read-only strict` needs `--upstream`, connects, and
fails any tool the upstream leaves un-annotated by `readOnlyHint`, so it belongs
in a rollout or smoke path. `--upstream` builds the same proxy `serve-upstream`
builds, so an absent tool fails here too and `--upstream-header` reaches an
authenticated one.

**A minted credential.** `--oauth2-client` declares a `client_credentials`
client that a header addresses as `{oauth2:<name>}`, for a hosted upstream whose
token is fetched rather than stored. See [oauth2.md](oauth2.md).

**As a guardfile.** The same proxy is stated in one reviewable file, which
`serve-upstream <spec.mcp.kdl>` serves and `lint` and `lint-upstream
<spec.mcp.kdl>` validate. A file beside `--upstream` or `--tool` is refused:
the file already states them.

```kdl
instructions { text "Search the published Tandem docs index." }

withhold "delete_doc" {
    reason "This surface is read-only by policy."
    alternative "get_doc"
}

mcp-upstream "ac.tandem/docs-mcp" {
    url "https://tandem.ac/mcp"
    transport streamable-http
    annotation-coverage partial annotated=7 silent=6
    auth header-token { header "Authorization"; prefix "Bearer "; value env "TOKEN" }
    can "search_docs"
    can "get_doc"
}
```

`url` is `--upstream`, each `can` is a `--tool`, and `auth` is the
guardfile-wide credential grammar lifted onto `--upstream-header`, resolving
through the same registry. `annotation-coverage` is optional and checked
against itself. Every other body node fails closed, `can` carries no schema,
and beside the node only `description` (umbra's own), `instructions`,
`oauth2-client` and `withhold` are projected: a proxy that silently ignored a
`confirm` would serve wider than the file reads. A file that grants nothing
lints clean and will not serve. `mcp-beaver pull` writes one from a registry
entry, see [pull.md](pull.md).

**`withhold` beside the node.** The same stub a REST guardfile mints, on the
proxy: a tool that appears in `tools/list`, states why it is not offered, names
a substitute, and refuses every call with a structured `verb_withheld` payload
while reaching no upstream. It is the reason to hand-author one of these files
rather than regenerate it, because the interesting thing about an allowlist is
usually the neighbour it leaves out. The checks run offline, against the
allowlist the file declares: a stub shadowing a `can` and a named alternative
the file does not grant both fail `lint`. `lint --methods` prints the stub as
`WITHHELD`, and `lint-upstream` prints only the allowlist, so a withheld
mutating verb never fails a `--read-only` screen. See
[guardfile-controls.md](guardfile-controls.md).

**The chart mounts one.** `runtime.mode: upstream` plus a `spec` carrying this
file mounts it at `/spec/<specName>.mcp.kdl` and runs `serve-upstream` against
that path, so a guardfile-stated proxy deploys the way a REST guardfile does.
The `upstream:` values block is refused beside it. See [chart.md](chart.md).

**umbra owns the grammar.** `mcpverb.ParseUpstream` reads the node and
`mcpverb.Classify` reports which shape a file carries before either parser
runs, so beaver states policy about a hosted MCP in the same dialect every
other consumer does. What stays here is the wiring: the `oauth2-client`
registry a credential addresses, the headers the proxy presents, and which
siblings this server can honour.

`auth` gained by moving. umbra parses `header-token`, `bearer`, `query-param`
and `none` through a value chain, where the hand-rolled parser took
`header-token` with one provider and address. The proxy presents headers, so
`header-token`, `bearer` and `none` serve; `query-param` refuses rather than
dropping a secret the file says to send, and a chain naming a second source
refuses because a header resolves one and never falls back.

**The node used to be `wrap mcp upstream`.** `wrap` reads every argument as a
command path, so that spelling parses as the command path `["mcp", "upstream",
"<name>"]` rather than failing, which is why the node became a sibling of
`wrap` instead of a form of it. A `pull`-generated file is cheap to regenerate.
A hand-authored one is a one-line edit: rename the top-level node, and change
nothing inside it.
