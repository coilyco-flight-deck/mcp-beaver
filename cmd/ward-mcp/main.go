// Command ward-mcp is the generic runtime that renders one `.mcp.kdl` spec into
// a guarded MCP server and matching HTTP tool API. It is the single static
// binary baked into every ward-mcp image; the spec is the only thing that
// varies. There is no per-guardfile Go and no per-server handler.
//
//	ward-mcp serve /spec/<name>.mcp.kdl --http :8080
//
// It parses the spec through cli-guard's opcore engine, projects one MCP tool
// plus one POST /api/{tool-name} endpoint per grant, and binds one HTTP
// listener. It never binds stdio: these run as remote pods reached by URL.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp/internal/mcpserver"
	internaltelemetry "forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp/internal/telemetry"
)

const shutdownTimeout = 5 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runContext(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ward-mcp:", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand. Only `serve` exists today; the pipeline's
// build/lock steps are cli-guard's and deploy's, not this runtime's.
func run(argv []string) error {
	return runContext(context.Background(), argv)
}

func runContext(ctx context.Context, argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("usage: ward-mcp serve <spec.mcp.kdl> [--http :8080] | ward-mcp serve-ssm <spec.mcp.kdl> [--http :8080] | ward-mcp serve-upstream --upstream <mcp-url> --tool <name> [--tool <name> ...]")
	}
	switch argv[0] {
	case "serve":
		return runServe(ctx, argv[1:])
	case "serve-upstream":
		return runServeUpstream(ctx, argv[1:])
	case "serve-ssm":
		return runServeSSM(ctx, argv[1:])
	default:
		return fmt.Errorf("unknown command %q (want: serve, serve-ssm, serve-upstream)", argv[0])
	}
}

// runServe parses the spec and binds the HTTP listener. The spec path is the one
// positional; --http sets the bind address (default :8080).
func runServe(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("http", ":8080", "HTTP listen address for the MCP server (/mcp streamable HTTP)")
	// The documented entrypoint writes the spec before --http
	// (`serve /spec/x.mcp.kdl --http :8080`), so reorder the flags ahead of the
	// positional - flag.Parse stops at the first non-flag argument otherwise.
	if err := fs.Parse(reorderFlagsFirst(argv)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("serve needs exactly one spec path, e.g. `serve /spec/forgejo.mcp.kdl --http :8080`")
	}
	specPath := fs.Arg(0)
	name := serverName(specPath)
	return withTelemetry(ctx, name, func() error {
		src, err := os.ReadFile(specPath) //nolint:gosec // operator-supplied trusted policy path
		if err != nil {
			return fmt.Errorf("read spec %q: %w", specPath, err)
		}
		srv, err := mcpserver.New(name, specPath, src)
		if err != nil {
			return fmt.Errorf("parse spec %q: %w", specPath, err)
		}

		fmt.Fprintf(os.Stderr, "ward-mcp: serving %s on %s (MCP /mcp, HTTP tools /api/{tool-name})\n", specPath, *addr)
		return serveHTTP(ctx, *addr, srv.Handler())
	})
}

// runServeUpstream binds the HTTP listener for a proxy server that exposes a
// selected subset of an upstream streamable-HTTP MCP server.
func runServeUpstream(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("serve-upstream", flag.ContinueOnError)
	addr := fs.String("http", ":8080", "HTTP listen address for the MCP server (/mcp streamable HTTP)")
	name := fs.String("name", "ward-mcp-upstream", "MCP server name")
	upstream := fs.String("upstream", "", "streamable-HTTP MCP upstream endpoint")
	connectTimeout := fs.Duration("connect-timeout", 0, "retry upstream startup until this timeout (0 fails immediately)")
	var tools stringSliceFlag
	fs.Var(&tools, "tool", "allowlisted upstream tool to expose (repeatable)")
	if err := fs.Parse(reorderFlagsFirst(argv)); err != nil {
		return err
	}
	if *upstream == "" {
		return fmt.Errorf("serve-upstream needs --upstream <mcp-url>")
	}
	if len(tools) == 0 {
		return fmt.Errorf("serve-upstream needs at least one --tool allowlist entry")
	}
	return withTelemetry(ctx, *name, func() error {
		srv, err := connectProxyWithRetry(ctx, *connectTimeout, time.Second, func(ctx context.Context) (*mcpserver.Server, error) {
			return mcpserver.NewProxy(ctx, *name, "", *upstream, tools, nil)
		})
		if err != nil {
			return fmt.Errorf("connect upstream %q: %w", *upstream, err)
		}
		defer func() { _ = srv.Close() }()
		fmt.Fprintf(os.Stderr, "ward-mcp: serving upstream proxy %s on %s (%d MCP and HTTP tools)\n", *upstream, *addr, len(tools))
		return serveHTTP(ctx, *addr, srv.Handler())
	})
}

// connectProxyWithRetry lets a proxy container start alongside an upstream
// sidecar without crash-looping while the sidecar publishes its MCP listener.
// A zero timeout retains the command's fail-fast behavior for direct use.
func connectProxyWithRetry(
	ctx context.Context,
	timeout time.Duration,
	retryInterval time.Duration,
	connect func(context.Context) (*mcpserver.Server, error),
) (*mcpserver.Server, error) {
	if timeout < 0 {
		return nil, fmt.Errorf("connect timeout must not be negative")
	}
	if timeout == 0 {
		return connect(ctx)
	}
	if retryInterval <= 0 {
		return nil, fmt.Errorf("retry interval must be positive")
	}

	retryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		srv, err := connect(retryCtx)
		if err == nil {
			return srv, nil
		}
		lastErr = err

		timer := time.NewTimer(retryInterval)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("timed out after %s: %w", timeout, lastErr)
		case <-timer.C:
		}
	}
}

// runServeSSM serves the spec-declared, exact-parameter SSM reader. AWS SDK
// credential discovery reads the static key injected into the container.
func runServeSSM(ctx context.Context, argv []string) error {
	fs := flag.NewFlagSet("serve-ssm", flag.ContinueOnError)
	addr := fs.String("http", ":8080", "HTTP listen address for the MCP server (/mcp streamable HTTP)")
	if err := fs.Parse(reorderFlagsFirst(argv)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("serve-ssm needs exactly one spec path")
	}
	specPath := fs.Arg(0)
	name := serverName(specPath)
	return withTelemetry(ctx, name, func() error {
		src, err := os.ReadFile(specPath) //nolint:gosec // operator-supplied trusted policy path
		if err != nil {
			return fmt.Errorf("read spec %q: %w", specPath, err)
		}
		srv, err := mcpserver.NewSSM(ctx, name, specPath, src)
		if err != nil {
			return fmt.Errorf("parse SSM spec %q: %w", specPath, err)
		}
		fmt.Fprintf(os.Stderr, "ward-mcp: serving guarded SSM reader %s on %s (MCP and HTTP tools)\n", specPath, *addr)
		return serveHTTP(ctx, *addr, srv.Handler())
	})
}

func withTelemetry(ctx context.Context, serviceName string, run func() error) (err error) {
	runtime, err := internaltelemetry.Setup(ctx, serviceName)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if shutdownErr := runtime.Shutdown(shutdownCtx); shutdownErr != nil {
			err = errors.Join(err, fmt.Errorf("shutdown OpenTelemetry: %w", shutdownErr))
		}
	}()
	return run()
}

func serveHTTP(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{Addr: addr, Handler: handler}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// reorderFlagsFirst moves flag tokens (and the value of the one known
// value-taking flag, --http) ahead of positional arguments, so `serve <spec>
// --http :8080` parses the same as `serve --http :8080 <spec>`. A bare `--`
// terminator passes through untouched.
func reorderFlagsFirst(argv []string) []string {
	var flags, positional []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		if a == "--" {
			positional = append(positional, argv[i:]...)
			break
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// A separated value (`--http :8080`) rides along with its flag;
			// the `--http=:8080` form already carries its value inline.
			if !strings.Contains(a, "=") && i+1 < len(argv) && !strings.HasPrefix(argv[i+1], "-") {
				flags = append(flags, argv[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// serverName derives the MCP serverInfo name from the spec filename, stripping
// the `.mcp.kdl` (or `.kdl`) suffix: `/spec/forgejo-issues.mcp.kdl` -> `forgejo-issues`.
func serverName(specPath string) string {
	base := filepath.Base(specPath)
	base = strings.TrimSuffix(base, ".kdl")
	base = strings.TrimSuffix(base, ".mcp")
	if base == "" {
		return "ward-mcp"
	}
	return base
}
