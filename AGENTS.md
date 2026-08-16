---
ward:
  workflow: merge-remote-main
---
# Agent instructions

## Scope

`mcp-beaver` turns a umbra Guardfile into a guarded MCP server with an
automatic matching HTTP tool API, and ships the generic runtime as an OCI
image plus Helm chart. The module, the binary, the chart, and the served
`mcp_beaver_info` tool all carry that name.

## The old name, and where it still belongs

This was `ward-mcp`, and the rename is done here. Two places keep the old
spelling on purpose and are not stragglers to sweep:

- **`wrap ward mcp <name>`** opens every guardfile. It is umbra's inline
  grammar rather than this project's name, and every deployed spec starts with
  it. It moves when umbra moves, not before.
- **A live resource name.** A Kubernetes selector label is immutable, so a
  release installed before the rename keeps its old names through
  `nameOverride: ward-mcp`. See [the chart](docs/chart.md).

Package installers name the old binary so they can mark it outdated and
uninstall it. That is the tap's and the bucket's business, not this repo's.

## Boundaries

Keep the runtime spec-driven and deny by absence. `umbra` owns guarded HTTP
execution. `mcp-beaver` owns MCP and HTTP tool projection plus transport.
`deploy` owns service configuration, inbound identity and authentication,
ingress, credentials, and rollout.

## Commands

Route development through Ward using the verbs in [`.ward/ward.yaml`](.ward/ward.yaml).

## Validation

Run `ward exec test` and the relevant chart or image checks before landing a
change. Never bypass commit hooks.

## Safety

Do not widen a Guardfile grant, tool allowlist, path restriction, or secret
boundary as incidental cleanup. Keep tokens and deployment identifiers out of
tracked files and logs.

## Release

Push validated work to canonical Forgejo `main`. CI publishes an immutable
source-SHA image. `deploy` consumes that exact reference.

## Checkout residency

This repo is not in Agent Compose's `repository-plan.yaml`, so it has no
resident checkout under `~/projects/<owner>/`. That is intentional. Work it
from a task-scoped temporary clone, and remove that clone once the work lands.

A temporary root can be purged at any time, so commit and push before pausing,
switching tasks, or ending a session. The remote is the only durable artifact.

## See also

* [README.md](README.md) - product boundary and quickstart.
* [docs/FEATURES.md](docs/FEATURES.md) - shipped runtime and chart inventory.
* [`.ward/ward.yaml`](.ward/ward.yaml) - allowlisted commands.
