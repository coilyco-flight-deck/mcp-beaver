# ward-mcp CI

The [`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) pipeline is the
whole automation surface: a **gate** that build/vet/tests the generic runtime on
every push and pull request, and a **publish** step that - on a push to `main`
only - builds the one runtime image and pushes it to the in-cluster registry
keyed by sha. It mirrors the fleet's other MCP source repos (`reddit-mcp`,
`node-stats-mcp`).

## `gate` job

Runs on `ubuntu-latest`, on every `push` and `pull_request`.

* **checkout + setup-go** - `actions/checkout@v4`, `actions/setup-go@v5` pinned to
  Go 1.25.
* **`GOPRIVATE=forgejo.coilysiren.me`** - cli-guard is a private forgejo module
  fetched anonymously, so `GOPRIVATE` keeps it off the public proxy and sumdb.
  The Dockerfile build sets the same var for its own cli-guard fetch.
* **build / vet / test** - `go build ./...`, `go vet ./...`, `go test ./...`. A
  red gate here is a real build or test breakage to fix in source.

## `publish` job

Runs on the `docker` label, `needs: [gate]`, guarded by
`if: github.event_name == 'push' && github.ref == 'refs/heads/main'` - never on a
pull request, never on a feature branch. A green source commit on `main` is what
produces `192.168.0.194:30500/ward-mcp:<sha>`.

* **docker CLI** - the `docker` runner resolves to `node:20-bookworm`, which
  ships no docker CLI, so the job pulls the static binary in and talks to the
  DinD sidecar over `DOCKER_HOST`.
* **resolve docker host** - the sidecar shares the runner pod's netns on `:2375`
  but the job container sits on a separate per-workflow bridge, so the step
  probes candidate hosts and pins the first that answers.
* **build + push** - `docker build` then `docker push` to the in-cluster
  registry. The DinD sidecar carries `--insecure-registry=192.168.0.194:30500`,
  so the plain-http push round-trips with no registry login or push secret.
* **no `:latest`** - the fleet keys rollouts by sha (`scripts/rollout.sh`), and
  the chart's `image.tag` is the resolved sha. The deploy CD resolves that sha
  and rolls it (deploy#61 / deploy#46).

The publish target `192.168.0.194:30500` is an **in-cluster** address. If the
runner executing the job cannot reach it, that is an infra reachability fact
(runner placement / registry route), not a code bug - land the gate green and
report the publish blocker precisely rather than forcing it.

## Enable the Actions unit before expecting runs (ward-mcp#10)

A valid workflow on the default branch does **not**, by itself, produce a run. A
run is queued only when the repo's **Actions unit is active at the moment a push
event lands**. If the unit is switched on *after* a batch of pushes, those pushes
never queued a task and CI reads as "never runs" - `total_count: 0` from
`GET /repos/{owner}/{repo}/actions/tasks`, even though the workflow parses fine
and the shared runner is green for peer repos.

The unit's state lives in `has_actions` on the repo:

* **check** - `ward ops forgejo repo get coilyco-flight-deck ward-mcp` and read
  `has_actions`.
* **toggle** - `ward ops forgejo repo edit coilyco-flight-deck ward-mcp
  --has_actions true` enables it.
* **first run** - once the unit is on, CI has still never been given a push to
  react to. Land a commit to `main` and confirm a task appears in
  `actions/tasks`. This doc's landing commit is that first push.
