# Guardfile siblings: controls

Opt-in controls stated beside `wrap`. Context nodes: [guardfile-siblings.md](guardfile-siblings.md).

## Query pins

`pin "<tool>" { query "<name>" env "<VAR>" }` fixes an outgoing query parameter
server-side, resolved at call time from `env`, `file`, or `literal`. The pinned
name is absent from the tool schema, so a caller can neither supply nor override
it. It is the spec-mode counterpart to the proxy's `--pin`: `set` writes body
values only, leaving a GET whose scope rides in the query string nowhere to go.
Pinning a caller-input parameter is a build error, and an unresolvable pin fails
the call rather than sending an unscoped request.

**Rate limit.** `rate-limit "1/1s"` states a per-server, process-wide outbound bucket. It
serialises rather than rejecting: a queued call is slower, a 503 is a failed
turn. The pod has one IP, so a public-good API otherwise sees one caller whose
rate is a whole community's. Grant-backed only, and a call queued past the
request deadline fails with a stated timeout.

**Response cache.** `cache "<tool-name>" ttl="15m"` reuses one grant's upstream answer for a
window, not the `ttlMs` on list results, which caches the inventory rather than
the data. Off by default. Keyed on the tool plus canonicalised arguments, and
outside the rate limiter so a hit spends no slot. Failed calls are never stored,
caching a destructive or `confirm`-gated grant is a build error.

**Reject empty.** `reject-empty "<tool>"` makes an empty result a tool error; `reject-empty-argument
"<tool>" field="<name>"` refuses a write carrying a blank field. Both off by
default. Empty is no content, whitespace, `null`, `""`, `[]`, or `{}` past the
`coverage` envelope; **`false` and `0` are answers**.

## Withheld verbs and confirmations

`withhold "<tool-name>" { reason ...; alternative ... }` mints a discoverable
stub for a verb left out on purpose: it appears in `tools/list`, states why,
names a substitute, and refuses every call with a structured `verb_withheld`
payload while reaching no upstream. A `coilyco.io/withheld` marker in `_meta`
separates stubs from live tools. `confirm "<tool-name>"` gates one tool behind a
Multi Round-Trip Request: the first call returns an input request, and the tool
runs only on a retry carrying an explicit accept.
