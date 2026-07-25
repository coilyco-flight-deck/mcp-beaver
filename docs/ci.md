# ward-mcp CI

The [`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) pipeline is the
whole automation surface: a **gate** that build/vet/tests the generic runtime on
every push and pull request, and a **publish** step that - on a push to `main`
only - builds the one runtime image and pushes it to the in-cluster registry
keyed by sha. It mirrors the fleet's other MCP source repos (`reddit-mcp`,
`node-stats-mcp`).

## `gate` job

Runs on the `docker` label, on every `push` and `pull_request`. `docker` is the
only label this Forgejo instance's runners advertise - `ubuntu-latest` is a
GitHub-mirror label (the `.github/*` side, executed by GitHub, not Forgejo), so
a gate pinned to it matches no Forgejo runner. The job now runs inside the
moving :release aos dev-base image, which already ships Go and the Docker CLI.

* **checkout + dev-base container** - `actions/checkout@v4`, then
  `container: forgejo.coilysiren.me/coilyco-flight-deck/agentic-os:release`.
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

* **dev-base container** - the job also runs inside
  `forgejo.coilysiren.me/coilyco-flight-deck/agentic-os:release`, which already
  ships the Docker CLI.
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

## Why CI never ran (ward-mcp#10)

CI showed `total_count: 0` from `GET /repos/{owner}/{repo}/actions/tasks` across
9+ merges to `main` - zero runs ever - though `.forgejo/workflows/ci.yml` was
valid on the default branch and the shared runner was green for peer repos. Two
facts had to line up for a run to queue, and this repo missed one:

* **the Actions unit must be active when the push event lands.** A valid workflow
  on the default branch does not, by itself, queue a run - the unit has to be on
  at the moment of the push. Check `has_actions` via
  `ward ops forgejo repo get coilyco-flight-deck ward-mcp`, toggle it with
  `ward ops forgejo repo edit coilyco-flight-deck ward-mcp --has_actions true`.
* **the gate has to name a label the Forgejo runner advertises.** The gate was
  pinned to `runs-on: ubuntu-latest`, which is a GitHub-mirror label - the
  Forgejo runners here only advertise `docker`. A gate no runner can match is the
  concrete difference from the green peers, all of which run on `docker`. The fix
  was pinning the gate to `docker` (above).

Once both hold, land a commit to `main` and confirm a task appears in
`actions/tasks`.
