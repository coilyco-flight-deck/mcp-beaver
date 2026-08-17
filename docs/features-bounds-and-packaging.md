# Bounds, operator HTTP, and packaging

The living inventory of what mcp-beaver ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. mcp-beaver turns a umbra Guardfile
into a guarded MCP server with an automatic matching HTTP tool API, distributed
as a runtime image plus a generic Helm chart.

## Request bounds

Nothing on this axis was bound before: `http.Server` was built with no
timeouts, the proxy client was nil (so `http.DefaultClient`, which has none),
and the SDK does not propagate HTTP cancellation to handlers by default. A
wedged upstream therefore held a request for as long as the caller would wait.

* **Per-call deadline** - `--request-timeout` (default 60s, 0 disables) bounds
  one tool call end to end, applied at the handler so it holds for the MCP
  transport and `POST /api/{tool-name}` alike, and for every protocol version.
  It rides the request context, so it aborts the outbound upstream call rather
  than only cutting the response.
* **Transport deadline** - the inbound request carries the per-call bound plus
  five seconds of headroom, so the tool always expires first and the runtime
  still has room to report the failure. Both expiring together produced an
  empty body, which reads as a crashed pod rather than a slow upstream.
* **Cancellation propagation** - a caller that goes away cancels the work.
  Applies to >= 2026-07-28 clients, which is why the per-call bound is not
  redundant with it.
* **Upstream client** - proxy mode bounds its own client at 45s when the caller
  supplied none, under the inbound deadline. A caller-set timeout is preserved.
  Spec mode was already bounded by opcore's 30s default client.
* **Connection guards** - 10s request-header timeout and a 120s idle timeout.
  No write timeout: it is absolute from the start of a request and would cut a
  legitimately slow upstream mid-response.
* **Attribution** - the MCP method, and the tool where there is one, are stamped
  on the transport span before dispatch, so a request that never returns still
  names what was in flight. `/healthz` is exempt from every deadline: a
  liveness probe a wedged upstream can fail turns one slow dependency into a
  restart loop.

## Operator HTTP

Non-MCP endpoints for runtime inspection and control. These are HTTP surfaces
for operators, not MCP tools:

* **Describe** - `GET /admin/describe` returns the loaded guardfile name/path,
  projected tool count, transport mode, upstream presence, and safe non-secret
  config facts.
* **Reload** - `POST /admin/reload` is explicit but currently restart-only. The
  runtime cannot safely hot-reload its guarded state in place, so the endpoint
  reports restart required instead.

Supported MCP methods: `initialize`, `notifications/initialized`,
`notifications/cancelled`, `ping`, `tools/list`, `tools/call`, `prompts/list`,
`prompts/get`, `resources/list`, `resources/read`, `resources/templates/list`,
and the 2026-07-28 `server/discover` and `subscriptions/listen`.

Resource and prompt support rides that generic MCP surface and adds no runtime
specific admin, lifecycle, reload, or control verb. That constraint is the one
to preserve as the surface grows: operator control stays on the `/admin`
endpoints above, off the protocol.

## Image

A single [`Dockerfile`](../Dockerfile) builds the one generic runtime image
(distroless, nonroot). The spec is mounted or COPYed in and named on the command
line - the same binary drives every `.mcp.kdl`. Building the image is a CI
consequence of a landed commit: the `publish` job in
[`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) builds the Dockerfile
on every push to `main` and publishes the private single-architecture image as
`forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver:<full-source-sha>`
(mount-not-bake, so one image serves every guardfile and publishes only when
runtime source changes). The source gate runs in the moving :release aos
dev-base image. The trusted deploy runner owns the package-write credential,
verifies the remote manifest, and hands the exact reference to deploy. Fleet
consumers use a separate read-only credential. See [ci.md](ci.md) for the gate
and publish walkthrough plus the Actions-unit enablement gotcha (mcp-beaver#10).

## See also

- [features-packaging.md](features-packaging.md) - the rest.
- [FEATURES.md](FEATURES.md) - the index.
