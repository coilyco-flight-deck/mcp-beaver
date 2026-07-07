# ward-mcp

**A cli-guard Guardfile, no handwritten code, becomes a Docker image that serves a working MCP over HTTP/SSE.**

ward-mcp renders a [cli-guard](https://github.com/coilysiren/cli-guard) Guardfile into a guarded MCP server, baked into an OCI image. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP handler - and no per-tool input schema, because cli-guard's engine derives it from the OpenAPI operation.

The spec configures **only the image interior**: which upstream, which auth, which grants become which tools. The image serves MCP over SSE/streamable-HTTP (never stdio - these run as remote k3s pods reached by URL). ward-mcp has **no relation to the ward codebase**, and uses [cli-mcp](https://github.com/coilysiren/cli-mcp) as a code reference only, not a dependency.

The exposed MCP surface is exactly the guardfile's grants: `never delete issue` means no `delete_issue` tool can ever be served, and `restrict owner matches coily*` bounds every path. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

## Quickstart (drafted, not yet implemented)

```sh
# lock the granted surface (cli-guard's existing online step), then build the image
ward mcp build examples/forgejo-issues.mcp.kdl

# run the image; it serves MCP over HTTP/SSE, token injected at run time
docker run -p 8080:8080 -e FORGEJO_TOKEN \
  forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp-forgejo-issues \
  serve --http :8080
```

The image serves `create_issue`, `get_issue`, `list_issues`, `comment_issue`, `close_issue` - each guarded, each scoped to `coily*` owners.

## Distributes as image + chart

The product ships two artifacts (ward-mcp#6): the generic runtime **image** above, and a generic **Helm chart** (`chart/`) that templates the k3s exposure. **Deploying** an MCP is then a values file plus `helm upgrade` - no per-guardfile image build, no per-service manifest fork:

```sh
helm upgrade --install skillsmp ward-mcp \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The `.mcp.kdl` rides in as chart values (a ConfigMap mounted into the runtime); the chart templates the Deployment, Service, token Secret wiring, and - for a public MCP - the Authelia-JWT route. The chart stays generic and **spec-opaque** (it never parses the guardfile), so the interior-only scope of the spec holds: the spec never reaches down into the chart, the chart never reaches up into the spec. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) consumes the chart per-MCP with one values file (deploy#61 skillsmp, deploy#46 forgejo) and owns the rollout, the same way it already hosts `steam-mcp`, `reddit-mcp`, and `node-stats-mcp`.

## Layout

* [`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) - the worked "hello world": Forgejo issue creation as an MCP. Its body is a plain cli-guard Guardfile.
* [`examples/*.values.yaml`](examples/) - reference deploy-side values: `skillsmp` (public, Authelia-gated read) and `forgejo-issues` (tailnet-only write).
* [`chart/`](chart/) - the generic ward-mcp Helm chart. See [`docs/chart.md`](docs/chart.md).
* [`docs/DESIGN.md`](docs/DESIGN.md) - the Guardfile -> image pipeline, the interior-only scope, the HTTP/SSE and safety model, and the remaining open question.
* [`docs/chart.md`](docs/chart.md) - the chart's templates, values reference, and the runtime contract it targets.

## Status

Draft. Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).
