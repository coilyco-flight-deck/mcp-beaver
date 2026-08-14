# ward-mcp CI

The [`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) pipeline is the
whole automation surface: a **gate** that build/vet/tests the generic runtime on
every push and pull request, and a **publish** step that - on a push to `main`
or an authorized manual dispatch of `main` - builds the one runtime image,
publishes it to Forgejo OCI under the full source sha, and verifies the remote
manifest.

## `gate` job

Runs on the `docker` label, on every `push` and `pull_request`. `docker` is the
only label this Forgejo instance's runners advertise - `ubuntu-latest` is a
GitHub-mirror label (the `.github/*` side, executed by GitHub, not Forgejo), so
a gate pinned to it matches no Forgejo runner. The job now runs inside the
moving :release aos dev-base image, which already ships Go and the Docker CLI.

* **checkout + dev-base container** - `actions/checkout@v4`, then
  `container: forgejo.coilysiren.me/coilyco-flight-deck/agentic-os:release`.
* **`GOPRIVATE=forgejo.coilysiren.me`** - umbra is a private forgejo module
  fetched anonymously, so `GOPRIVATE` keeps it off the public proxy and sumdb.
  The Dockerfile build sets the same var for its own umbra fetch.
* **build / vet / test** - `go build ./...`, `go vet ./...`, `go test ./...`. A
  red gate here is a real build or test breakage to fix in source.

## `publish` job

Runs on the trusted `deploy` label, `needs: [gate]`, guarded by
the `push` or `workflow_dispatch` event plus `refs/heads/main` - never on a pull
request, never on a feature branch. The manual path recovers a commit whose
original push did not queue Actions without inventing a local image or empty
source commit. A green source commit on `main` is what produces
`forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp:<full-source-sha>`.

* **trusted host runner** - the main-only lane owns Docker and receives the
  package-write credential.
* **build + push** - `scripts/publish-image.sh` creates a temporary Docker
  config, authenticates through password-stdin, builds one source-sha tag, and
  pushes it to Forgejo OCI.
* **remote proof** - `docker manifest inspect` must resolve the exact pushed
  reference before the job succeeds.
* **no `:latest`** - the fleet keys rollouts by sha (`scripts/rollout.sh`), and
  the deploy bundle consumes the exact full reference through a separate
  read-only credential.

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
