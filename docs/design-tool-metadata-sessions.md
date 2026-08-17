# Upstream session reuse

How mcp-beaver holds one upstream MCP session across calls, and what keeps it alive. Split from the derived-tool-metadata page.

### One upstream session, reused (mcp-beaver#67)

Upstream mode compares the upstream tool fingerprint against its startup
baseline before every call, so a surface that drifts fails closed rather than
serving a contract the operator never reviewed. That check runs over the
**long-lived session opened at startup**, never a fresh one.

A second session was what the drift check used to dial, and it made every
`serve-upstream` deployment in the fleet non-functional at call time: real Node
MCP servers answer a second `notifications/initialized` with HTTP 400, while a
Go MCP server accepts it, so mcp-beaver fronting mcp-beaver passed and
mcp-beaver fronting Playwright did not. The regression test therefore uses a
fixture that rejects a second handshake rather than another Go server.

Reuse also removes the round trip that never bought anything here. A
`serve-upstream` deployment co-locates its upstream as a digest-pinned sidecar,
so the upstream cannot change its tools while the pod lives, and a rollout
replaces the pod along with the session.

### The session must outlive a slow call, and survive losing one (mcp-beaver#79)

One session serving every call makes the session a single point of failure, and
two things had to change before that was safe.

**A stream is not a request, so `http.Client.Timeout` is the wrong bound.** It
covers reading the response body, and a streamable-HTTP MCP response **is** a
body that stays open. A 45s client timeout therefore killed any tool call whose
stream ran longer - a cold Chromium launch takes that on its own - and the abort
took the upstream session with it. Every later call then failed instantly with
`session not found`. That is how a playwright deployment recorded **0 successes
over 24 hours at p95 16.8ms**: the fast identical errors were not the tools
failing, they were one poisoned session answering. The bound is now
`ResponseHeaderTimeout`, which is time-to-first-byte and leaves the stream
alone. A hung upstream still fails, and the per-call deadline in
`withToolDeadline` bounds the rest through the request context - which is what
mcp-beaver#49 actually needed.

**A lost session is replaced rather than fatal.** The failing call still fails:
a tool call may already have reached the upstream and had its answer lost, and
replaying it would turn a timeout into a duplicate action. The **next** call
gets a live session. That is the difference between one bad minute and a
24-hour outage.

The reconnect deliberately does **not** re-snapshot the baseline. Re-reading it
would adopt whatever the upstream serves now as the reviewed contract, which is
exactly the drift the check exists to catch. Schema drift is never retried
either - it is a decision about the upstream, not a transport failure.

## See also

- [design-tool-metadata.md](design-tool-metadata.md) - derived metadata and PDF extraction.
- [DESIGN.md](DESIGN.md) - the index.
