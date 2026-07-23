// Command ward-mcp is the generic runtime that renders one `.mcp.kdl` spec into
// a guarded MCP server over HTTP. It is the single static binary baked into
// every ward-mcp image; the spec is the only thing that varies. There is no
// per-guardfile Go and no per-server handler.
//
//	ward-mcp serve /spec/<name>.mcp.kdl --http :8080
//
// It parses the spec through cli-guard's opcore engine, projects one MCP tool
// per grant, and binds an HTTP listener that speaks MCP over the SDK-backed
// streamable HTTP transport. It never binds stdio: these run as remote pods
// reached by URL.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp/internal/mcpserver"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ward-mcp:", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand. Only `serve` exists today; the pipeline's
// build/lock steps are cli-guard's and deploy's, not this runtime's.
func run(argv []string) error {
	if len(argv) == 0 {
		return fmt.Errorf("usage: ward-mcp serve <spec.mcp.kdl> [--http :8080] | ward-mcp serve-ssm <spec.mcp.kdl> [--http :8080] | ward-mcp serve-upstream --upstream <mcp-url> --tool <name> [--tool <name> ...]")
	}
	switch argv[0] {
	case "serve":
		return runServe(argv[1:])
	case "serve-upstream":
		return runServeUpstream(argv[1:])
	case "serve-ssm":
		return runServeSSM(argv[1:])
	default:
		return fmt.Errorf("unknown command %q (want: serve, serve-ssm, serve-upstream)", argv[0])
	}
}

// runServe parses the spec and binds the HTTP listener. The spec path is the one
// positional; --http sets the bind address (default :8080).
func runServe(argv []string) error {
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

	src, err := os.ReadFile(specPath) //nolint:gosec // operator-supplied trusted policy path
	if err != nil {
		return fmt.Errorf("read spec %q: %w", specPath, err)
	}
	srv, err := mcpserver.New(serverName(specPath), specPath, src)
	if err != nil {
		return fmt.Errorf("parse spec %q: %w", specPath, err)
	}

	fmt.Fprintf(os.Stderr, "ward-mcp: serving %s on %s (SDK-backed MCP over /mcp)\n", specPath, *addr)
	server := &http.Server{Addr: *addr, Handler: srv.Handler()}
	return server.ListenAndServe()
}

// runServeUpstream binds the HTTP listener for a proxy server that exposes a
// selected subset of an upstream streamable-HTTP MCP server.
func runServeUpstream(argv []string) error {
	fs := flag.NewFlagSet("serve-upstream", flag.ContinueOnError)
	addr := fs.String("http", ":8080", "HTTP listen address for the MCP server (/mcp streamable HTTP)")
	name := fs.String("name", "ward-mcp-upstream", "MCP server name")
	upstream := fs.String("upstream", "", "streamable-HTTP MCP upstream endpoint")
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
	srv, err := mcpserver.NewProxy(context.Background(), *name, "", *upstream, tools, nil)
	if err != nil {
		return fmt.Errorf("connect upstream %q: %w", *upstream, err)
	}
	fmt.Fprintf(os.Stderr, "ward-mcp: serving upstream proxy %s on %s (%d tools)\n", *upstream, *addr, len(tools))
	server := &http.Server{Addr: *addr, Handler: srv.Handler()}
	return server.ListenAndServe()
}

// runServeSSM serves the spec-declared, exact-parameter SSM reader. AWS SDK
// credential discovery reads the static key injected into the container.
func runServeSSM(argv []string) error {
	fs := flag.NewFlagSet("serve-ssm", flag.ContinueOnError)
	addr := fs.String("http", ":8080", "HTTP listen address for the MCP server (/mcp streamable HTTP)")
	if err := fs.Parse(reorderFlagsFirst(argv)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("serve-ssm needs exactly one spec path")
	}
	specPath := fs.Arg(0)
	src, err := os.ReadFile(specPath) //nolint:gosec // operator-supplied trusted policy path
	if err != nil {
		return fmt.Errorf("read spec %q: %w", specPath, err)
	}
	srv, err := mcpserver.NewSSM(context.Background(), serverName(specPath), specPath, src)
	if err != nil {
		return fmt.Errorf("parse SSM spec %q: %w", specPath, err)
	}
	fmt.Fprintf(os.Stderr, "ward-mcp: serving guarded SSM reader %s on %s\n", specPath, *addr)
	server := &http.Server{Addr: *addr, Handler: srv.Handler()}
	return server.ListenAndServe()
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
