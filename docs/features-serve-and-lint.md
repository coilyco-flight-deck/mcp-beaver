# Serving and linting

The living inventory of what mcp-beaver ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. mcp-beaver turns a umbra Guardfile
into a guarded MCP server with an automatic matching HTTP tool API, distributed
as a runtime image plus a generic Helm chart.

## `mcp-beaver serve <spec.mcp.kdl> --http :addr`

The generic local runtime. One static binary renders any `.mcp.kdl` spec into
a guarded MCP server over the official MCP Go SDK's streamable HTTP transport
at `/mcp` and automatically exposes the identical tool arguments at
`POST /api/{tool-name}`. No per-guardfile Go, no per-server handler. It never
binds stdio - these run as remote pods reached by URL.

* **Spec parse** - `opcore.ParseInline` (umbra `http/opcore`, pinned
  `v0.131.0`) parses the mcp-beaver inline grammar: `wrap` header, `base-url`,
  `auth`, `restrict`, and each
  `can <verb> <resource> { path/query/body/set }` grant. Flat body declarations
  remain optional string shorthand. Body blocks preserve typed scalars, scalar
  arrays, nested objects, required fields, and unconstrained raw object or
  array subtrees. They can instead project required nested string inputs onto
  fresh top-level JSON keys without forwarding undeclared input. Query blocks
  preserve string, boolean, integer, number, and
  scalar-array types plus numeric bounds, array length bounds, required fields,
  mutually-exclusive groups, and safe local aliases for upstream parameter
  names. The `.mcp.kdl` is the whole contract. Method is inferred from the verb,
  path params from the `{template}`.
* **Grant → tool projection** - each `Descriptor` becomes one MCP tool and
  one matching HTTP endpoint named `verb_resource`, its `inputSchema` derived
  (draft-07) from the grant's
  path/query/body, and its description taken from `describe` or derived as a
  user-goal sentence. mcp-beaver also derives a human title, read-only,
  destructive, idempotent, and open-world annotations from the operation's
  HTTP behavior, plus a `{coverage: ..., result: ...}` output schema.
  Initialization includes compact server instructions for the policy boundary.
  `internal/mcpserver`.
* **Coverage before payload** - every grant-backed result leads with a
  `coverage` block and carries the payload under `result`, in both the text and
  the structured content. A consuming harness bounds a tool result by keeping
  the front and discarding the tail, so a caveat serialized last is destroyed
  first and deterministically, and the model then answers from rows with no
  caveat. Coverage states `truncated` (always false, stated rather than
  omitted), `bytes`, `over_budget` past the smallest measured consumer cap, and
  `items` naming every array in the payload and its length - a count in meaning
  is what changes an answer, where a byte total does not. Enforced by the
  envelope being a struct rather than a map, so field order is a contract
  instead of an alphabetical accident. What the runtime cannot enforce - an
  upstream that leaves an array unbounded, or that answers a failed read with
  zero - is recorded in [DESIGN.md](DESIGN.md) rather than left implicit.
* **Typed query routing** - MCP query arguments retain their JSON types when
  mcp-beaver passes them to opcore. Scalars keep their existing wire spelling,
  arrays become repeated upstream keys in caller order, and type, bound,
  required-field, array-length, and mutual-exclusion violations fail before
  the upstream receives a request. Flat string query specs stay compatible.
* **Guarded execute** - a `tools/call` routes the MCP arguments onto
  `opcore.Args` (path/query → URL, body → JSON) without flattening nested body
  objects or arrays, then fires the self-guarding `opcore.Operation.Execute`:
  metachar gate, `restrict` allowlist, base-url, and env-token auth are the
  engine's, never re-implemented here. A denied or failed call returns as a
  tool result with `isError` set.
* **Built-in reader metadata** - the exact-parameter SSM tools advertise
  read-only, non-destructive, idempotent, open-world behavior and a specific
  structured parameter output schema. Passthrough mode preserves upstream
  titles, schemas, annotations, and results without reclassifying them.
* **Deny-by-absence** - the served surface is exactly the `can` grants. An
  unwritten grant is an absent tool; that is the deletion guard.

## `mcp-beaver lint <spec.mcp.kdl>`

The offline validation surface. It is `serve` minus the listener and minus
telemetry: read the spec, build the same server, print the minted tool names,
exit. No network, so it runs in a sealed clone and in CI.

* **Owning-loader validation** - lint builds the server through the same
  constructor `serve` uses rather than calling `opcore.ParseInline` directly,
  so it validates the grant-to-tool projection as well as the KDL parse. A
  well-formed file whose grants collide on one tool name fails here.
* **Tool-name output** - a clean spec exits 0 and writes the projected tool
  names to stdout, sorted, one per line. This is how a consumer repo reads its
  served surface off the owning loader instead of writing a second parser for
  the same guardfile. A rejected spec exits non-zero with the failure on
  stderr and writes nothing to stdout.
* **Warnings on stderr** - two facts that are invisible from every other
  surface, so a spec carrying one lints identically to a working spec. An
  unknown verb resolving to POST by fallthrough, and a `resource` stating no
  `audience`. Both are legitimate choices, so both warn rather than fail, and
  both keep off stdout so a warning never edits the diffable surface. The
  fallthrough warning reads opcore's own `MethodInferred`, so a grant that
  states `method` is owed no warning.
* **Stated HTTP method** - `method "POST"` inside a `can` body picks the method
  outright and leaves the verb free to name the tool well. The verb otherwise
  does both jobs, and they collide on a read served over POST - ordinary for a
  search API with a structured request body - which forced a create-shaped verb
  and a tool named `create_web_search` for a call that creates nothing. The
  verb table stays the default when `method` is absent. A stated `DELETE` marks
  the grant destructive whatever the verb is called, since the confirmation
  gate keys off the effect rather than the spelling, and anything outside
  GET / POST / PUT / PATCH / DELETE / HEAD fails closed. Grammar owned by
  umbra; pinned from v0.148.0.
* **Scope** - the `wrap` inline grammar the serving runtime renders.
  `serve-ssm` policies use a separate grammar and are not lintable through
  this path.

## See also

- [features-guardfile-siblings.md](features-guardfile-siblings.md)
- [features-conformance-and-upstream.md](features-conformance-and-upstream.md)
- [features-observability.md](features-observability.md)
- [features-bounds-and-packaging.md](features-bounds-and-packaging.md)
- [FEATURES.md](FEATURES.md) - the index.
