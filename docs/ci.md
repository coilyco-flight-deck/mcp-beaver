# CI

[`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) is the whole
automation surface: a **gate** that builds, vets, and tests the runtime and
validates the chart and examples on every push and pull request, and a
**publish** step that, on a push to `main` or an authorized manual dispatch of
`main`, builds the image, publishes it to Forgejo OCI under the full source
sha, and verifies the remote manifest.

## gate

Runs on the `docker` label, the only label this Forgejo instance's runners
advertise, inside the moving `:release` aos dev-base image which already ships
Go and the Docker CLI. `GOPRIVATE=forgejo.coilysiren.me` keeps umbra, a private
module fetched anonymously, off the public proxy and sumdb, and the Dockerfile
sets the same var for its own fetch. Then `go build`, `go vet`, `go test`.

Three more verbs follow, through `just` so a verb cannot pass on a laptop and
differ here. `lint-examples` keeps a committed example guardfile from rotting.
`helm-lint-chart` covers the chart in both its plain and `app`-bearing value
shapes. `check-app-mount` renders the chart, materializes the mount the way
kubelet projects it, and lints the result with the real runtime: a chart change
and a guardfile change both reach production without touching a Go file, and
`go test` sees neither. The image carries go, helm, yq, and just, so the three
need nothing installed. See [apps.md](apps.md).

## publish

Runs on the trusted `deploy` label with `needs: [gate]`, guarded by the `push`
or `workflow_dispatch` event plus `refs/heads/main`. Never on a pull request,
never on a feature branch. The manual path recovers a commit whose original
push did not queue Actions.

`scripts/publish-image.sh` creates a temporary Docker config, authenticates
through password-stdin, builds one source-sha tag, and pushes it. Then
`docker manifest inspect` must resolve the exact pushed reference before the
job succeeds. There is no `:latest`: the fleet keys rollouts by sha.

## Why CI never ran (mcp-beaver#10)

Two facts must line up for a run to queue, and this repo missed one.

The **Actions unit must be active when the push lands**. A valid workflow on
the default branch does not queue a run by itself. Check `has_actions` and
toggle it through the forgejo operator verbs.

The **gate must name a label the runner advertises**. It was pinned to
`ubuntu-latest`, a GitHub-mirror label the Forgejo runners do not carry, so it
matched no runner. Pinning to `docker` fixed it.
