---
ward:
  workflow: merge-remote-main
---
# Agent instructions

## Scope

`ward-mcp` turns a umbra Guardfile into a guarded MCP server with an
automatic matching HTTP tool API, and ships the generic runtime as an OCI
image plus Helm chart.

## Boundaries

Keep the runtime spec-driven and deny by absence. `umbra` owns guarded HTTP
execution. `ward-mcp` owns MCP and HTTP tool projection plus transport.
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
