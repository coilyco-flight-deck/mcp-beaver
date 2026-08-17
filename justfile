# Per-repo task manifest. Run `just` (or `just --list`) to see every verb.
#
# Recipes take trailing arguments directly: `just <verb> a b`, where the
# retired form was `ward exec <verb> -- a b`.
#
# One line of comment per recipe on purpose: just reads only the LAST comment
# line above a recipe, so a wrapped description silently truncates to its tail.
#
# `ward exec` is retired. `.ward/ward.yaml` survives carrying catalog metadata
# only, because the catalog hooks upstream in agentic-os pin that exact path.

set positional-arguments

# Default target: list every available recipe.
default:
    @just --list --unsorted

# Build all Go packages.
build *ARGS:
    @bash scripts/ward-command.sh build "$@"

# Run the Go test suite.
test *ARGS:
    @bash scripts/ward-command.sh test "$@"

# Run go vet across the tree.
vet *ARGS:
    @bash scripts/ward-command.sh vet "$@"

# Reconcile Go module dependencies.
tidy *ARGS:
    @bash scripts/ward-command.sh tidy "$@"

# Format the Go source tree.
fmt *ARGS:
    @bash scripts/ward-command.sh fmt "$@"

# Build the generic mcp-beaver runtime image locally.
image-build *ARGS:
    @docker build -t mcp-beaver:local . "$@"

# Validate the trusted Forgejo OCI publisher shell contract.
check-publish *ARGS:
    @bash -n scripts/publish-image.sh "$@"

# Pin umbra and reconcile Go module dependencies. Pass an optional version after `--`.
pin-umbra *ARGS:
    @bash scripts/ward-command.sh pin-umbra "$@"

# Serve the skillsmp example spec on localhost:18080 for local MCP handshake checks.
serve-example *ARGS:
    @bash scripts/ward-command.sh serve-example "$@"

# Lint every committed example guardfile, so a shipped reference cannot rot.
lint-examples *ARGS:
    @bash scripts/ward-command.sh lint-examples "$@"

# Lint the auth-neutral mcp-beaver chart.
helm-lint-chart *ARGS:
    @helm lint chart --set-file spec=examples/skillsmp.mcp.kdl -f examples/skillsmp.values.yaml "$@"

# Render the default ClusterIP chart shape.
helm-template-clusterip *ARGS:
    @helm template mcp-beaver chart --namespace mcp-beaver --set-file spec=examples/skillsmp.mcp.kdl -f examples/skillsmp.values.yaml "$@"

# Render the optional NodePort chart shape.
helm-template-nodeport *ARGS:
    @helm template mcp-beaver chart --namespace mcp-beaver --set-file spec=examples/forgejo-issues.mcp.kdl -f examples/forgejo-issues.values.yaml "$@"

# Render the allowlisted upstream-proxy chart shape.
helm-template-upstream *ARGS:
    @helm template mcp-beaver chart --namespace mcp-beaver -f examples/upstream.values.yaml "$@"

# Render the spec-mode sidecar shape, wrapping a co-located non-MCP process.
helm-template-sidecar *ARGS:
    @helm template mcp-beaver chart --namespace mcp-beaver --set-file spec=examples/sidecar.mcp.kdl -f examples/sidecar.values.yaml "$@"
