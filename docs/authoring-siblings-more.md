# Guardfile siblings, continued

Split from [authoring-siblings.md](authoring-siblings.md).

protocol-level `ping`. It is on by default: every field it returns is already
reachable through `initialize`, `tools/list`, and the list methods, so
withholding it protects nothing, and a probe present on only some servers
teaches an agent nothing from its absence on the rest. Rename it with
`server-info name="status"`, or remove it with `server-info disabled`.

`extract` turns a PDF an upstream returns into text an agent can read. A lot of
authoritative reference material - government statistics, standards bodies,
regulatory filings, equipment documentation - is published only as PDF, so a
grant reaching one otherwise returns bytes no model can use.

```kdl
extract "get_report" as="pdf-text" max-pages="20"
```

The grant must also declare `raw-response`, or opcore decodes the body as JSON
and the call fails on the first byte of `%PDF`. That is a build error rather
than a call-time surprise.

Bounded on three axes, because a PDF is the one input a granted upstream hands
this runtime that is arbitrarily large and arbitrarily structured: a 32MB gate
before the parser opens anything, a page bound defaulting to 20 and ceilinged
at 200, and a parse that runs off the request goroutine so a wedged document
returns a stated timeout rather than holding the handler open. Malformed and
encrypted documents are clean tool errors, and a parser panic becomes one
rather than taking the pod down. The coverage block gains
`pages: {shown, total}`, so a bounded read cannot be mistaken for the whole
document.

Text only. Table extraction and OCR are three very different amounts of work
with three very different dependency footprints, and `as="pdf-text"` names this
one so the next is additive. The in-process-versus-sidecar posture and the
library choice are recorded in [DESIGN.md](DESIGN.md).

`cache` names a **projected tool name** and reuses its upstream answer for the
declared window. It is off by default and opt-in per grant, because how stale
an answer may be is a property of the upstream that no default can guess. It is
also not the `ttlMs` / `cacheScope` on list results, which cache the tool
inventory rather than the data.

```kdl
cache "get_store_app_details" ttl="15m"
```

Every tool call is otherwise a live upstream request, so N identical questions
from a community produce N identical calls from one pod IP. That was a
politeness problem while every upstream was a keyless public good; against a
metered one it is a bill. Caching is the only control that lowers the bill
without lowering capability - a `rate-limit` bounds the rate and can only make
the tool refuse.

Entries are keyed on the tool plus its canonicalised arguments, so two
spellings of the same object hit the same entry and two different questions
never do. A cache sits outside the rate limiter, so a hit spends no slot. A
failed call is never stored, and caching a destructive grant or a
`confirm`-gated one is a build error rather than a surprise at call time. The
store is in-process, so it dies with the pod and two replicas keep two copies.

`confirm` names a **projected tool name** and gates it behind a Multi
Round-Trip Request: the first call returns an input request rather than acting,
and the tool runs only if the client retries with an explicit accept. A decline
or a dismissal never reaches the upstream. It is per tool rather than automatic
on every mutation because these deploy headless, where a prompt on every write
would wedge the service. Naming a tool the spec does not mint is an error - a
confirmation attached to nothing looks like a gate that is not there.

## See also

- [authoring-siblings.md](authoring-siblings.md) - the first half.
- [FEATURES.md](FEATURES.md) - what ships today.
