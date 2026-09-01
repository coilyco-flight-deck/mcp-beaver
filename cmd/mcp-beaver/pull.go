package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver/internal/mcpserver"
)

// runPull writes the guardfile for one registry server: the entry's remote,
// the tool surface it serves right now, and an allowlist decided by the
// upstream's own readOnlyHint at the requested scope. See docs/pull.md.
func runPull(ctx context.Context, out io.Writer, argv []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	registry := fs.String("registry", mcpserver.DefaultRegistry, "MCP registry base URL")
	upstream := fs.String("upstream", "", "skip the registry and connect to this streamable-HTTP endpoint; the name is then the file's own")
	scopeFlag := fs.String("scope", string(mcpserver.ScopeReadOnly), "which tools to allow, as `scope`: read-only, read-write, or all")
	outPath := fs.String("o", "", "write the guardfile here instead of stdout")
	var headerFlags stringSliceFlag
	fs.Var(&headerFlags, "upstream-header", "present a header to the upstream, as `<name>=<template>` where {env:VAR} resolves server-side (repeatable)")
	var oauth2Flags stringSliceFlag
	fs.Var(&oauth2Flags, "oauth2-client", "declare a client_credentials client a header may address as {oauth2:<name>} (repeatable)")
	if err := fs.Parse(reorderFlagsFirst(argv)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("pull needs exactly one registry name, e.g. `pull ac.tandem/docs-mcp`")
	}
	scope, err := mcpserver.ParseScope(*scopeFlag)
	if err != nil {
		return err
	}
	providers, err := upstreamProviders(oauth2Flags)
	if err != nil {
		return err
	}
	headers, err := parseUpstreamHeaders(headerFlags, providers)
	if err != nil {
		return err
	}
	if err := mcpserver.PreflightUpstreamHeaders(ctx, headers, providers); err != nil {
		return err
	}
	pulled, err := mcpserver.Pull(ctx, fs.Arg(0), mcpserver.PullOptions{
		Registry:  *registry,
		Upstream:  *upstream,
		Headers:   headers,
		Providers: providers,
	})
	if err != nil {
		return err
	}
	text, err := mcpserver.RenderUpstreamGuardfile(pulled, scope)
	if err != nil {
		return err
	}
	// Parsed back before it is written, so `pull` never emits a file its own
	// `lint` would refuse.
	if _, err := mcpserver.ParseUpstreamSpec(*outPath, []byte(text)); err != nil {
		return fmt.Errorf("generated guardfile does not parse, which is a bug: %w", err)
	}
	if *outPath == "" {
		_, err = io.WriteString(out, text)
		return err
	}
	return os.WriteFile(*outPath, []byte(text), 0o644) //nolint:gosec // operator-chosen output path
}

// proxyInputs is what a proxy needs, whichever surface stated it: the flags
// `serve-upstream` and `lint-upstream` always took, or a `wrap mcp upstream`
// guardfile that carries the same facts in a reviewable file.
type proxyInputs struct {
	name         string
	upstream     string
	tools        []string
	headers      []mcpserver.UpstreamHeader
	providers    mcpserver.ProviderSet
	instructions string
}

// upstreamFlagInputs is the flag half. The name is left for the caller.
type upstreamFlagInputs struct {
	upstream string
	tools    []string
	headers  []string
	oauth2   []string
}

// resolveProxyInputs reads the guardfile when one positional names it and the
// flags otherwise, and refuses a mix: a file that says one allowlist beside a
// flag that says another has no reviewable answer.
func resolveProxyInputs(command, specPath string, flags upstreamFlagInputs) (proxyInputs, error) {
	if specPath == "" {
		providers, err := upstreamProviders(flags.oauth2)
		if err != nil {
			return proxyInputs{}, err
		}
		headers, err := parseUpstreamHeaders(flags.headers, providers)
		if err != nil {
			return proxyInputs{}, err
		}
		return proxyInputs{upstream: flags.upstream, tools: flags.tools, headers: headers, providers: providers}, nil
	}
	var stated []string
	if flags.upstream != "" {
		stated = append(stated, "--upstream")
	}
	if len(flags.tools) > 0 {
		stated = append(stated, "--tool")
	}
	if len(flags.headers) > 0 {
		stated = append(stated, "--upstream-header")
	}
	if len(flags.oauth2) > 0 {
		stated = append(stated, "--oauth2-client")
	}
	if len(stated) > 0 {
		return proxyInputs{}, fmt.Errorf("%s takes a guardfile or %s, not both: the file already states them", command, strings.Join(stated, ", "))
	}
	src, err := os.ReadFile(specPath) //nolint:gosec // operator-supplied trusted policy path
	if err != nil {
		return proxyInputs{}, fmt.Errorf("read spec %q: %w", specPath, err)
	}
	if proxy, err := mcpserver.IsUpstreamSpec(src); err != nil {
		return proxyInputs{}, fmt.Errorf("invalid spec %q: %w", specPath, err)
	} else if !proxy {
		return proxyInputs{}, fmt.Errorf("spec %q does not open `wrap mcp upstream`; `serve` and `lint` render a REST guardfile", specPath)
	}
	spec, err := mcpserver.ParseUpstreamSpec(specPath, src)
	if err != nil {
		return proxyInputs{}, fmt.Errorf("invalid spec %q: %w", specPath, err)
	}
	return proxyInputs{
		name:         serverName(specPath),
		upstream:     spec.URL,
		tools:        spec.Tools,
		headers:      spec.Headers,
		providers:    spec.Providers,
		instructions: spec.Instructions,
	}, nil
}

// lintUpstreamSpec is `lint` for a `wrap mcp upstream` guardfile: the offline
// parse plus the allowlist printed sorted, matching `lint-upstream`, and a
// `-` in the second column because a proxied tool resolves no verb and
// carries no widget.
func lintUpstreamSpec(out io.Writer, specPath string, src []byte, second bool) error {
	spec, err := mcpserver.ParseUpstreamSpec(specPath, src)
	if err != nil {
		return fmt.Errorf("invalid spec %q: %w", specPath, err)
	}
	for _, name := range sortedCopy(spec.Tools) {
		line := name
		if second {
			line += "\t-"
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}
