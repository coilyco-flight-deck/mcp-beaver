.PHONY: build test vet tidy fmt pin-cli-guard helm-lint-chart helm-template-clusterip helm-template-nodeport helm-template-upstream

export GOPRIVATE = forgejo.coilysiren.me
CLI_GUARD_REF ?= v0.122.0

build: ## Build all Go packages.
	go build ./...

test: ## Run the Go test suite.
	go test ./...

vet: ## Run go vet across the tree.
	go vet ./...

tidy: ## Reconcile Go module dependencies.
	go mod tidy

fmt: ## Format the Go source tree.
	gofmt -w cmd internal

pin-cli-guard: ## Pin cli-guard and reconcile Go module dependencies.
	go get forgejo.coilysiren.me/coilyco-flight-deck/cli-guard@$(CLI_GUARD_REF)
	go mod tidy

helm-lint-chart: ## Lint the auth-neutral ward-mcp chart.
	helm lint chart --set-file spec=examples/skillsmp.mcp.kdl -f examples/skillsmp.values.yaml

helm-template-clusterip: ## Render the default ClusterIP chart shape.
	helm template ward-mcp chart --namespace ward-mcp --set-file spec=examples/skillsmp.mcp.kdl -f examples/skillsmp.values.yaml

helm-template-nodeport: ## Render the optional NodePort chart shape.
	helm template ward-mcp chart --namespace ward-mcp --set-file spec=examples/forgejo-issues.mcp.kdl -f examples/forgejo-issues.values.yaml

helm-template-upstream: ## Render the allowlisted upstream-proxy chart shape.
	helm template ward-mcp chart --namespace ward-mcp -f examples/upstream.values.yaml
