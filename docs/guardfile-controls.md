# Guardfile siblings: controls

Opt-in controls stated beside `wrap`. Context nodes are in
[guardfile-siblings.md](guardfile-siblings.md).

## Query pins

`pin "<tool>" { query "<name>" env "<VAR>" }` fixes an outgoing query parameter
server-side, resolved at call time from `env`, `file`, or `literal`. The pinned
name is absent from the tool schema, so a caller can neither supply nor
override it. This is the spec-mode counterpart to the proxy's `--pin`, and it
exists because `set` writes fixed body values only, leaving a GET whose scope
rides in the query string nowhere to put it. Pinning a parameter the grant also
declares as caller input is a build error, and an unresolvable pin fails the
call rather than sending an unscoped request.

**Rate limit.** `rate-limit "1/1s"` states a per-server, process-wide outbound bucket. It
serialises rather than rejecting: a queued call is slower, a 503 is a failed
turn. The pod has one IP, so a public-good API otherwise sees one caller whose
rate is a whole community's traffic. Grant-backed tools only. A call that would
queue past the request deadline fails with a stated timeout.

**Response cache.** `cache "<tool-name>" ttl="15m"` reuses one grant's upstream answer for a
window. Not the `ttlMs` on list results, which cache the tool inventory rather
than the data. Off by default, because correctness varies entirely by upstream.
Keyed on the tool plus canonicalised arguments. It sits outside the rate
limiter, so a hit spends no slot. Failed calls are never stored, and caching a
destructive or `confirm`-gated grant is a build error. The store is in-process
and dies with the pod.

## Withheld verbs and confirmations

`withhold "<tool-name>" { reason ...; alternative ... }` mints a discoverable
stub for a verb left out on purpose: it appears in `tools/list`, states why,
names a substitute, and refuses every call with a structured `verb_withheld`
payload while reaching no upstream. A `coilyco.io/withheld` marker in `_meta`
lets a client separate stubs from live tools without reading prose. `confirm
"<tool-name>"` gates one tool behind a Multi Round-Trip Request: the first call
returns an input request, and the tool runs only on a retry carrying an
explicit accept. Per tool rather than blanket, because these run headless.
