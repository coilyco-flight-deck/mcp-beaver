# Guardfile siblings

The living inventory of what mcp-beaver ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. mcp-beaver turns a umbra Guardfile
into a guarded MCP server with an automatic matching HTTP tool API, distributed
as a runtime image plus a generic Helm chart.

## Guardfile siblings: instructions, resources, prompts, server-info, PDF extraction, response cache, withheld verbs, confirmations

Top-level nodes stated beside `wrap`, outside the frozen inline grammar
`opcore.ParseInline` owns. Each fails closed on an unknown property or child.
All are opt-in except `server-info`, which is on by default and opts out.

* **Server instructions** - `instructions { text ... }` states what this server
  is for, published under the shared policy sentence in
  `InitializeResult.Instructions`. Every generated server used to publish only
  that shared sentence, so a client holding four of them learned that each
  exposes policy-approved tools - true of all four and distinguishing none. The
  policy floor still reaches every server and still serialises first. Bounded
  at 500 characters, because a consumer that renders this into the model's
  prompt pays for it every turn, once per rostered server. A guardfile that
  declares nothing publishes exactly what it published before.
* **Resources** - `resource "<name>" uri=... { text ... }` serves static
  content on `resources/read`. Inline only by design: a resource proxying an
  upstream read would be a second, unguarded egress path beside the grants.
  Claude Code surfaces these as `@` mentions. `audience "assistant"` and
  `priority=0.9` emit the MCP annotations an agent harness gates on when
  deciding to pull a resource into a model's context unprompted. Stating no
  audience means no such harness includes the resource, so `lint` warns:
  serving correctly and reaching nobody are the same thing from outside.
* **Prompts** - `prompt "<name>" { argument ...; text ... }` serves a message
  template on `prompts/get` with `{arg}` substitution. A missing required
  argument is an error, since a half-filled prompt reads as a complete one.
  Claude Code surfaces these as slash commands.
* **Server info** - one read-only tool reporting the server's identity, mode,
  and tool inventory. It reaches no upstream and restores the liveness probe
  2026-07-28 removed along with `ping`. **On by default**: every field it
  returns is already reachable through `initialize`, `tools/list`, and the
  list methods, so opting out withholds nothing, and a probe present on only
  some servers lets an agent read no meaning from its absence on the rest.
  `server-info name="status"` renames it, `server-info disabled` removes it.
  It counts itself, so `lint` and `tools/list` report the same surface.
* **Query pins** - `pin "<tool>" { query "<name>" env "<VAR>" }` fixes an
  outgoing query parameter server-side, resolved at call time from `env`,
  `file`, or `literal`. The pinned name is absent from the tool schema, so a
  caller can neither supply nor override it. This is the spec-mode counterpart
  to the proxy's `--pin`, and it exists because `set` writes fixed **body**
  values only, leaving a GET whose scope rides in the query string nowhere to
  put it. Pinning a parameter the grant also declares as a caller input is a
  build error, and an unresolvable pin fails the call rather than sending an
  unscoped request. The concrete case is Steam's `steamid`, where a declared
  field would turn "this account's library" into "any account's library".
* **Rate limit** - `rate-limit "1/1s"` states a per-server, process-wide
  outbound bucket in `<count>/<duration>` form. It serialises rather than
  rejecting: a queued tool call is slower, a 503 is a failed turn. The pod has
  one IP, so a public-good API otherwise sees one caller whose request rate is
  the sum of a whole community's traffic. Grant-backed tools only - the info
  tool and withheld stubs reach no upstream and are not charged. A call that
  would queue past the request deadline fails with a stated timeout rather than
  holding a slot.
* **PDF text extraction** - `extract "<tool-name>" as="pdf-text" max-pages="20"`
  turns a PDF an upstream returns into text an agent can read. A lot of
  authoritative reference material - government statistics, standards bodies,
  regulatory filings, equipment documentation - is published only as PDF, so a
  grant reaching one used to return bytes no model could use. Requires the
  grant to declare `raw-response`, checked at build. Bounded on three axes: a
  32MB size gate before the parser, a page bound defaulting to 20 and ceilinged
  at 200, and a parse that runs off the request goroutine so a wedged document
  returns a stated timeout rather than holding the handler. Malformed and
  encrypted documents are clean tool errors, and a parser panic is recovered
  into one. The coverage block gains `pages: {shown, total}`, so a bounded read
  cannot be mistaken for the whole document. Text only - table extraction and
  OCR are separate decisions. The in-process-versus-sidecar posture and the
  library choice are recorded in [DESIGN.md](DESIGN.md).
* **Response cache** - `cache "<tool-name>" ttl="15m"` reuses one grant's
  upstream answer for a window. **Not** the `ttlMs` / `cacheScope` on list
  results, which cache the tool inventory rather than the data. Opt-in per
  grant and off by default, because correctness varies entirely by upstream and
  a stale answer beats a slow one only where the author says so. Keyed on the
  tool plus its canonicalised arguments, so two spellings of the same object
  hit the same entry and two different questions never do. Sits outside the
  rate limiter, so a hit spends no slot - a rate limit bounds the rate and can
  only make a tool refuse, where a cache is the one control that lowers a
  metered bill without lowering capability. Failed calls are never stored,
  caching a destructive grant or a `confirm`-gated one is a build error, and
  the store is in-process and dies with the pod - the same objection #69 raises
  about the rate limiter, with the same answer if a durable store lands.
* **Withheld verbs** - `withhold "<tool-name>" { reason ...; alternative ... }`
  mints a discoverable stub for a verb left out on purpose. It appears in
  `tools/list`, states why in its description, names a substitute where one
  exists, and refuses every call with a structured `verb_withheld` payload
  while reaching no upstream. A `coilyco.io/withheld` marker in `_meta` lets a
  client separate stubs from live tools without reading prose. Absence
  otherwise means four different things and an agent has to guess which.
* **Confirmations** - `confirm "<tool-name>"` gates one tool behind a Multi
  Round-Trip Request: the first call returns an input request, and the tool
  runs only on a retry carrying an explicit accept. Anything else never
  reaches the upstream. Per tool rather than blanket, because these run
  headless where a prompt on every write would wedge the deployment.

## See also

- [features-serve-and-lint.md](features-serve-and-lint.md)
- [features-conformance-and-upstream.md](features-conformance-and-upstream.md)
- [features-observability.md](features-observability.md)
- [features-bounds-and-packaging.md](features-bounds-and-packaging.md)
- [FEATURES.md](FEATURES.md) - the index.
