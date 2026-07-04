# ward-mcp design draft

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The one-line pitch

**A cli-guard Guardfile, no handwritten code, becomes a Docker image running a working MCP server.**

`ward mcp build forgejo-issues.guardfile.kdl` bakes the guardfile plus one generic runtime into an OCI image whose ENTRYPOINT speaks the Model Context Protocol. Every `can` grant becomes one MCP tool. No per-server Go, no per-server Dockerfile, no per-server MCP handler, and - the part that surprises people - **no per-tool input schema**, because the engine derives it.

## Not built on ward. Built on cli-guard.

ward-mcp has **no relation to the ward codebase.** It is a driver over [cli-guard](https://github.com/coilysiren/cli-guard)'s three-layer spec engine and its [cli-mcp](https://github.com/coilysiren/cli-mcp) sibling. ward-kdl (in the ward repo) is a *different* consumer of the same cli-guard engine; ward-mcp reaches the engine directly and shares nothing with ward but the upstream framework.

cli-guard already turns a Guardfile into a guarded surface, in three layers ([specverb.md](https://github.com/coilysiren/cli-guard/blob/main/docs/specverb.md)):

* **L0 - upstream spec.** The vendor's OpenAPI/Swagger truth, embedded and pruned to the granted operations.
* **L1 - policy IR.** The compiled operation set. verb+resource resolves to an operation by convention; `op` overrides.
* **L2 - Guardfile.** The human authoring layer, pure KDL data, parsed never evaluated.

The engine carries zero upstream knowledge, so **one engine drives every spec, no code changes.** Its `specverb.Build` mounts each grant as a generic guarded action: path params become positionals, query and body fields become typed flags, all validated before the wire call. cli-guard's own `kdl-specs` driver (`gen`/`lock`/`skew`/`build`/`run`) wraps that into a no-code **CLI** the way `uv` wraps a Python build.

## ward-mcp is the MCP sibling of the kdl-specs driver

Where `kdl-specs run` renders the guardfile into a guarded urfave/cli binary, **ward-mcp renders the same guardfile into a guarded MCP server and bakes it into an image.** Same L2 guardfile, same L0 spec lock, same auth - a different L3 target:

| | cli-guard `kdl-specs` | **ward-mcp** |
|---|---|---|
| target | urfave/cli binary | MCP server (stdio / streamable-HTTP) |
| a grant becomes | a CLI leaf (`... create issue`) | an MCP tool (`create_issue`) |
| input schema | positionals + typed flags | JSON-schema, **derived from the same op** |
| artifact | `bin/<name>` | OCI image |
| ships via | `build` / `run` | CI on push, like `publish-image` |

## The safety story - the guardfile IS the tool surface

This is the point. A ward-mcp server **cannot exceed its guardfile**, because the guardfile is not a policy bolted beside the tools - it is the *only* source of the tools.

* Every `can` grant is one tool. There is no way to declare a tool the guardfile does not grant.
* `never delete issue` means no `delete_issue` tool is minted. A deny beats an allow.
* `restrict owner matches coily*` bounds every `{owner}` path param on the MCP surface exactly as on the CLI.
* The argv metachar gate still runs on inputs that compose into the URL (path + query); body fields (issue titles, markdown) are exempt, as they are on the CLI.

So handing a write-capable MCP to an agent (deploy#40's ask) is defensible: the blast radius is one small reviewable file, and it is enforced at the transport, not trusted to the model. Audit the guardfile, know the surface.

## The pipeline (Guardfile -> image -> MCP)

```
forgejo-issues.guardfile.kdl ─┐
forgejo.swagger.v1.json.lock  ├─► ward mcp build ─► OCI image ─► `ward-mcp serve` (stdio / HTTP)
specverb.lock                 ─┘        (CI on push)              one MCP tool per `can` grant
```

1. **Author** the `*.guardfile.kdl` (the only hand-edited artifact).
2. **Lock** (cli-guard's existing online step) fetches upstream Swagger, prunes it to the granted ops + their transitive `$ref` schemas, and writes the two committed locks (`<spec>.lock.json`, `specverb.lock`). This is reused verbatim - the same locks a CLI build uses.
3. **Build** bakes the guardfile + the two locks + the single static `ward-mcp` runtime into an image. The runtime is reused across every guardfile, the way one cli-guard engine drives every spec.
4. **Serve** - ENTRYPOINT `ward-mcp serve /spec/<name>.guardfile.kdl`. At startup the runtime parses the guardfile, resolves each grant to an operation, and registers the survivors as MCP tools with schemas derived from the pruned op. A tool call is validated against that schema, run through the guard, resolved to an HTTP request, signed with the run-time-injected secret, and fired.
5. **Secrets** inject at run time (`value env FORGEJO_TOKEN`, or an `ssm` ref resolved on the host via cli-guard's value-provider registry), never baked in.

### "Automatic" = a CI consequence of a landed guardfile

A push to `main` touching a `.guardfile.kdl` triggers the image build in CI, the way a Dockerfile edit triggers `publish-image` today. No `buildx --push` by hand. Landing the guardfile **is** publishing the image.

## The spec dialect

There is nothing new to learn - it **is** the cli-guard Guardfile. See [`examples/forgejo-issues.guardfile.kdl`](../examples/forgejo-issues.guardfile.kdl). The whole surface is the `can` / `never` grants; input schemas are derived, not authored.

Open questions for review:

1. **Filename convention.** Keep `.guardfile.kdl` (it literally is one, and the SAME file can also drive a CLI), or adopt a `.mcp.kdl` marker so a repo can carry MCP-only guardfiles distinctly? Leaning `.guardfile.kdl` - one file, two targets is the strongest version of the pitch.
2. **Tool naming.** `create_issue` (verb_resource) vs `forgejo_create_issue` (wrap-group-prefixed) when an agent mounts several ward-mcp servers at once.
3. **Transport.** stdio only (agent spawns the container per session), streamable-HTTP (long-lived shared server), or both, selectable at serve time.
4. **Relationship to cli-mcp.** Does ward-mcp *become* cli-mcp's image target, or wrap cli-mcp as a library? Needs a read of cli-mcp (GitHub, not in substrate) before deciding.

## First milestone (matches deploy#40)

Narrowest end-to-end slice: the `forgejo-issues` guardfile above, one grant (`can create issue`), building to an image an agent adds to its MCP config to open an issue on a `coily*` repo. Everything else (comment, list, close, other upstreams) is additive once the spine works, and needs no new grammar.
