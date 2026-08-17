# Guardfile siblings

Authoring guidance for mcp-beaver guardfiles, split out of the README.

## Optional guardfile siblings

A guardfile can state a few optional nodes beside `wrap`. `opcore.ParseInline`
reads only `wrap`, so these never touch the frozen grammar. Each is opt-in and
fails closed: declare none and the server behaves exactly as before.

```kdl
server-info                                   // mints `mcp_beaver_info`
confirm "create_issue" message="Create this issue upstream?"
cache "get_issue" ttl="15m"                   // reuse one read's answer

instructions {
    text "Issues, pull requests and repository metadata on the Coilyco Forgejo."
    text "Reach for it to read or file tracker work."
}

resource "oncall" uri="beaver://runbook/oncall" mime="text/markdown" priority=0.9 {
    audience "assistant"
    description "First response for an upstream 5xx"
    text "1. Call mcp_beaver_info to confirm the pod is serving."
    text "2. Check the upstream status page."
}

prompt "triage" title="Triage an incident" {
    description "Walk the on-call first-response steps"
    argument "service" description="Which service is failing" required=#true
    text "You are triaging {service}. Read beaver://runbook/oncall first."
}

wrap ward mcp forgejo {
    // ... unchanged
}
```

`instructions` says what **this** server is for. It is published in
`InitializeResult.Instructions`, under the shared policy sentence every
generated server carries - read before mutate, schemas are contracts, safety
annotations are hints rather than authorization. That sentence is true of every
guardfile, which is exactly why it cannot tell four beaver servers apart on one
roster; this is where a guardfile says which dam you are at.

Keep it short. A consumer that renders instructions into the model's prompt
pays for them on every turn, once per rostered server, so the text is bounded
at 500 characters and a spec over budget fails at build rather than at read
time. A guardfile that declares nothing publishes exactly what it published
before, so no deployed image changes until its spec opts in.

`resource` and `prompt` are what Claude Code renders as `@` mentions and slash
commands. Resource content is inline only: a resource that proxied an upstream
read would be a second egress path beside the `can` grants, and the grants are
the whole security model.

A resource's `audience` and `priority` are the MCP annotations an agent harness
reads to decide whether to pull the content into a model's context without
being asked. A harness that gates on `audience` treats an unannotated resource
as not meant for the model and skips it, so reference material written for an
agent needs `audience "assistant"` stated. Both are optional, and a resource
declaring neither serves exactly the bytes it did before.

`server-info` mints one read-only tool that reports the server's identity,
mode, and tool inventory without reaching any upstream. It also restores a
liveness probe, which clients otherwise lost when MCP 2026-07-28 removed the

## See also

- [authoring-siblings-more.md](authoring-siblings-more.md) - the remaining siblings.
- [FEATURES.md](FEATURES.md) - what ships today.
