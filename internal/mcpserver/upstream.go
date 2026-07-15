package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type proxyBackend struct {
	mu         sync.Mutex
	endpoint   string
	httpClient *http.Client
	session    *mcp.ClientSession
	allowlist  []string
	baseline   map[string]string
	selected   []*mcp.Tool
	driftErr   error
}

func newProxyBackend(ctx context.Context, upstreamURL string, allowTools []string, httpClient *http.Client) (*proxyBackend, error) {
	if strings.TrimSpace(upstreamURL) == "" {
		return nil, fmt.Errorf("ward-mcp: upstream endpoint is empty")
	}
	if len(allowTools) == 0 {
		return nil, fmt.Errorf("ward-mcp: upstream allowlist is empty")
	}
	allowlist := uniqueStrings(allowTools)
	if len(allowlist) != len(allowTools) {
		return nil, fmt.Errorf("ward-mcp: duplicate upstream tool allowlist entry")
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "ward-mcp", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             upstreamURL,
		HTTPClient:           httpClient,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect upstream MCP %q: %w", upstreamURL, err)
	}

	p := &proxyBackend{
		endpoint:   upstreamURL,
		httpClient: httpClient,
		session:    session,
		allowlist:  allowlist,
		baseline:   map[string]string{},
	}
	if err := p.snapshot(ctx); err != nil {
		_ = session.Close()
		return nil, err
	}
	return p, nil
}

func (p *proxyBackend) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session == nil {
		return nil
	}
	err := p.session.Close()
	p.session = nil
	return err
}

func (p *proxyBackend) selectedTools() []*mcp.Tool {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*mcp.Tool, 0, len(p.selected))
	for _, tool := range p.selected {
		out = append(out, cloneTool(tool))
	}
	return out
}

func (p *proxyBackend) toolHandler(name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := p.ensureFresh(ctx, name); err != nil {
			return toolError(err), nil
		}
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
			}
		}
		resp, err := p.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return toolError(err), nil
		}
		return resp, nil
	}
}

func (p *proxyBackend) snapshot(ctx context.Context) error {
	res, err := p.probeTools(ctx)
	if err != nil {
		return fmt.Errorf("list upstream tools: %w", err)
	}
	tools := make([]*mcp.Tool, 0, len(p.allowlist))
	baseline := make(map[string]string, len(p.allowlist))
	for _, name := range p.allowlist {
		tool, err := findUpstreamTool(res.Tools, name)
		if err != nil {
			return err
		}
		cloned := toolSpecFromUpstream(tool)
		fp, err := fingerprintTool(cloned)
		if err != nil {
			return fmt.Errorf("fingerprint upstream tool %q: %w", name, err)
		}
		baseline[name] = fp
		tools = append(tools, cloned)
	}
	p.mu.Lock()
	p.selected = tools
	p.baseline = baseline
	p.mu.Unlock()
	return nil
}

func (p *proxyBackend) ensureFresh(ctx context.Context, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.driftErr != nil {
		return p.driftErr
	}
	res, err := p.probeTools(ctx)
	if err != nil {
		return fmt.Errorf("refresh upstream tools: %w", err)
	}
	tool, err := findUpstreamTool(res.Tools, name)
	if err != nil {
		return err
	}
	fp, err := fingerprintTool(tool)
	if err != nil {
		return fmt.Errorf("fingerprint upstream tool %q: %w", name, err)
	}
	want, ok := p.baseline[name]
	if !ok {
		return fmt.Errorf("ward-mcp: tool %q is not allowlisted", name)
	}
	if fp != want {
		p.driftErr = fmt.Errorf("ward-mcp: upstream schema drift for tool %q", name)
		return p.driftErr
	}
	if !slices.Contains(p.allowlist, name) {
		return fmt.Errorf("ward-mcp: tool %q is not allowlisted", name)
	}
	return nil
}

func (p *proxyBackend) probeTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	if strings.TrimSpace(p.endpoint) == "" {
		return nil, fmt.Errorf("ward-mcp: upstream endpoint is empty")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "ward-mcp-probe", Version: "0.1.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             p.endpoint,
		HTTPClient:           p.httpClient,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = session.Close() }()
	return session.ListTools(ctx, nil)
}

func findUpstreamTool(tools []*mcp.Tool, name string) (*mcp.Tool, error) {
	for _, tool := range tools {
		if tool != nil && tool.Name == name {
			return tool, nil
		}
	}
	return nil, fmt.Errorf("ward-mcp: upstream tool %q was not found or is no longer exposed", name)
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
