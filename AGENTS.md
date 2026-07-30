# Agent instructions

## Scope

`ward-mcp` turns a cli-guard Guardfile into a guarded MCP server and ships the
generic runtime as an OCI image plus Helm chart.

## Boundaries

Keep the runtime spec-driven and deny by absence. `cli-guard` owns guarded HTTP
execution. `ward-mcp` owns MCP projection and transport. `deploy` owns service
configuration, ingress, credentials, and rollout.

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

## See also

* [README.md](README.md) - product boundary and quickstart.
* [docs/FEATURES.md](docs/FEATURES.md) - shipped runtime and chart inventory.
* [`.ward/ward.yaml`](.ward/ward.yaml) - allowlisted commands.
