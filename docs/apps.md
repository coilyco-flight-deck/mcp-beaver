# MCP Apps

An MCP App is an interactive HTML widget a host renders in place of a tool's
text result. The `app` node declares one, and mcp-beaver serves it: the bytes
at a `ui://` URI with mime `text/html;profile=mcp-app`, and
`_meta.ui.resourceUri` on each tool that carries it.

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

Beaver is the producer end. It declares the widget and the link. The host owns
the sandboxed iframe, the postMessage bridge, and every rendering decision, and
the widget itself is the author's code.

What a guarded server adds over an unguarded one is the tool surface: a widget
calls tools back through the host, and the tools that exist here are the `can`
grants and nothing else. The widget's blast radius is the same audited file as
the agent's.

## The body is a file

`file` resolves against the guardfile's own directory, so a spec and its
widgets move together, and a deployment mounting the spec mounts the widget
beside it. An absolute path is refused, because a guardfile is mounted into a
container that has no such path and the failure would land at startup on the
deployment rather than at lint on the author's machine.

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

Both ride on the resource, never on the tool, and hosts ignore them there.
Every omitted csp list is `'none'` at the host, so a self-contained bundle
needs no `csp` node at all and declaring one narrows nothing that was open.

- `connect` - fetch, XHR, WebSocket origins
- `resource` - images, scripts, stylesheets, fonts, media
- `frame` - nested iframes
- `base-uri` - allowed document base URIs

`permission` takes `camera`, `microphone`, `geolocation`, or `clipboardWrite`.
A host MAY honour them on the iframe, so a widget still needs feature
detection.

`domain` asks the host for a dedicated sandbox origin, which is worth stating
only when a widget needs a stable origin for OAuth callbacks or an API-key
allowlist. Its format is host-specific.

## Worked example

[`examples/guardfile-siblings.mcp.kdl`](../examples/guardfile-siblings.mcp.kdl)
declares `thing-card`, and
[`examples/widgets/things.html`](../examples/widgets/things.html) is the widget
it serves: a dependency-free page doing the `ui/initialize` handshake and
rendering the tool result the host pushes in.

Wire shapes here are measured against the published reference servers
`@modelcontextprotocol/server-basic-vanillajs` and
`@modelcontextprotocol/server-map`, and against the types shipped in
`@modelcontextprotocol/ext-apps` (specification 2026-01-26). Tool `_meta`
carries the flattened `ui/resourceUri` beside the nested object, matching both,
so a host of either era finds the link.
