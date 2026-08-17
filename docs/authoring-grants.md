# Authoring grants

Authoring guidance for mcp-beaver guardfiles, split out of the README.

## Choosing a grant's HTTP method

A verb picks the method by convention: `get` and `list` reach GET, `create`
reaches POST, `edit` reaches PATCH. The verb is also the tool name, and those
two jobs collide whenever an upstream serves a **read over POST**, which is
ordinary for any search API taking a structured request body. Reaching POST
used to mean naming the tool `create_web_search` for a call that creates
nothing, and a model pattern-matches hardest on exactly that string.

`method` states it outright and leaves the verb free to name the tool well:

```kdl
can search web {
    method "POST"
    path "/search"
    body {
        map "search" to="query"
    }
}
```

That mints `search_web` over POST. The verb table stays the default when
`method` is absent, so nothing existing changes.

Three things follow from stating it. A stated method is a decision, so `lint`
owes it no unknown-verb warning - which is the point, since the alternative was
relying on a verb **not** being in the table, and adding it later would have
silently changed a deployed guardfile's method. A stated `DELETE` marks the
grant destructive whatever the verb is called, because the confirmation gate
keys off the effect rather than the spelling. And anything outside
GET / POST / PUT / PATCH / DELETE / HEAD fails closed at build.

## Naming an outgoing query parameter

A `query` field's local name is what the caller sees in the tool schema. When
the upstream spells the parameter differently, `upstream=` states the outgoing
name:

```kdl
can search artist {
    path "/ws/2/artist"
    query "search" upstream="query"
    query "fmt" "limit"
}
```

That call sends `?query=<search>`, and the tool schema advertises `search`.

The aliased form is the only way to reach an upstream parameter named `query`,
`output`, `dry-run` or `body-file`, because those four are umbra's per-leaf
engine flags and a local input may not shadow one. The reserved check reads the
**local** name, so aliasing satisfies it rather than working around it. Search
APIs hit this constantly: MusicBrainz, Solr-backed endpoints, and
Elasticsearch-style ones all take their search string in `query`.

Two guards keep the alias honest. Repeating the local name in `upstream=` is an
error rather than a no-op, and two fields aliasing onto the same outgoing name
fail closed at build rather than silently dropping one.

## Authoring request bodies

Use the flat shorthand when every caller-supplied body field is an optional
string:

```kdl
body "title" "body"
```

Use a body block when the MCP schema needs types, required fields, arrays, or
nested objects:

```kdl
body {
    field "title" type="string" required=true
    field "published" type="boolean"
    array "labels" items="string"
    object "options" {
        field "limit" type="integer" required=true
        field "compact" type="boolean"
    }
}
```

Use `raw=true` only as the escape hatch for an object or array whose internal
shape is intentionally unconstrained:

```kdl
body {
    object "vendorPayload" raw=true required=true
    array "events" raw=true
}
```

Raw values are still real JSON objects or arrays in `tools/call` arguments.
They are not JSON strings. mcp-beaver advertises the object or array type plus
`x-opcore-raw: true`, then preserves the supplied subtree when it builds the
upstream request.

To migrate an existing `body "title" "body"` declaration, replace it with a
block containing `field "title" type="string"` and
`field "body" type="string"`. Both remain optional, so the behavior is
unchanged. Add `required=true` only when the upstream contract requires the
field. Replace a string field with `object`, `array`, or a scalar `field` type
only when callers should send that JSON type.

## See also

- [authoring-siblings.md](authoring-siblings.md) - guardfile siblings.
- [authoring-upstream-and-otel.md](authoring-upstream-and-otel.md) - upstream and telemetry.
- [FEATURES.md](FEATURES.md) - what ships today.
