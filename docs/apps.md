# MCP Apps

An MCP App is an interactive HTML widget a host renders in place of a tool's
text result. The `app` node declares one, and mcp-beaver serves it: the bytes at
a `ui://` URI with mime `text/html;profile=mcp-app`, and `_meta.ui.resourceUri`
on each tool that carries it.

```kdl
app "thing-card" uri="ui://thing-card/mcp-app.html" file="widgets/things.html" prefers-border=#true {
    description "The thing this tool returned, as a card"
    tool "get_thing"
    tool "search_things" visibility="app"
    csp {
        connect "https://*.cesium.com"
        resource "https://cdn.example.com"
    }
    permission "clipboardWrite"
}
```

`mcp-beaver lint --apps <spec>` prints the widget uri beside each tool, `-`
where a tool carries none, so the App surface is readable offline rather than
through a live handshake.

## What beaver owns, and what it does not

Beaver is the producer end: it declares the widget and the link. The host owns
the sandboxed iframe, the postMessage bridge, and every rendering decision, and
the widget is the author's code.

What a guarded server adds is the tool surface. A widget calls tools back
through the host, and the tools that exist here are the `can` grants and
nothing else, so the widget's blast radius is the same audited file as the
agent's.

## The body is a file

`file` resolves against the directory of the guardfile that declared it, so a
spec and its widgets move together. An absolute path is refused: a guardfile is
mounted into a container that has no such path, and the failure would land at
startup on the deployment rather than at lint on the author's machine.

This is the one place `app` departs from
[`resource`](guardfile-siblings.md), which is inline-only. That rule is about
egress: a resource proxying an upstream read would be a second, unguarded path
beside the grants. Reading a local file once at startup reaches no network and
opens no such path. A real widget is a few hundred KB of bundled script, which
no KDL string literal should hold. One body is bounded at 10 MB.

## Linking tools

`tool` is what makes a host fetch the widget at all. Without it nothing carries
`_meta.ui.resourceUri` and the resource is read by nobody.

Three cases are build errors rather than warnings, because each one serves
cleanly and renders for nobody while linting identically to a working spec:

- an `app` naming no `tool`
- an `app` naming a tool this spec does not mint
- two apps claiming the same tool, since a tool carries one widget

`visibility` states who may reach the tool: `model` for the agent, `app` for
the widget, both when omitted. It is a declaration the host acts on, not
something this runtime enforces. The enforceable bound stays the grants.

Not every tool needs a widget. A host decides per call, so a mixed surface is
the normal shape.

## csp and permissions

Both ride on the resource, never on the tool, and hosts ignore them there. Every
omitted csp list is `'none'`, so a self-contained bundle needs no `csp` node and
declaring one narrows nothing that was open.

- `connect` - fetch, XHR, WebSocket origins
- `resource` - images, scripts, stylesheets, fonts, media
- `frame` - nested iframes
- `base-uri` - allowed document base URIs

`permission` takes `camera`, `microphone`, `geolocation`, or `clipboardWrite`,
and a host MAY honour them, so a widget still needs feature detection. `domain`
asks for a dedicated sandbox origin, worth stating only when a widget needs a
stable one for OAuth callbacks or an API-key allowlist.

## Deploying one

The chart mounts the guardfile at `/spec/<specName>.mcp.kdl`, and a widget lands
beside it at the path the guardfile names. One `widgets` entry per `app file=`:

```sh
helm upgrade --install things mcp-beaver \
  --set-file spec=things.mcp.kdl \
  --set-file widgets[0].content=widgets/things.html \
  --set widgets[0].path=widgets/things.html
```

`path` is spelled exactly as the guardfile spells it. A missing `content`, a
path holding `..`, and two paths colliding on one ConfigMap key are all
template-time failures naming the offending path. `just check-app-mount` renders
the chart, materializes the mount as kubelet projects it, and lints that with the
real runtime, so the seam is checked rather than assumed.

Widgets are base64-encoded into the guardfile's own ConfigMap, so they cost a
third more than their file size against its 1 MiB cap, and the chart refuses at
template time rather than letting the apply fail.

A `flatten`ed guardfile is no different, and [inherit.md](inherit.md) says why.

## Worked example

[`examples/guardfile-siblings.mcp.kdl`](../examples/guardfile-siblings.mcp.kdl)
declares `thing-card`, and served beside it
[`examples/widgets/things.html`](../examples/widgets/things.html) is a
dependency-free page doing the `ui/initialize` handshake and rendering the tool
result the host pushes in.

Wire shapes are measured against the published `server-basic-vanillajs` and
`server-map`, and the types in `@modelcontextprotocol/ext-apps` (specification
2026-01-26). Tool `_meta` carries the flattened `ui/resourceUri` beside the
nested object, matching both, so a host of either era finds the link.
