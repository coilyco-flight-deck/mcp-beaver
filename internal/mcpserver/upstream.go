package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upstreamClientTimeout bounds one request to the proxied upstream MCP.
//
// The serving path passed a nil client, which the transport resolves to
// http.DefaultClient - and that has no timeout at all. Spec mode was already
// bounded, because opcore's default client carries one; proxy mode was the
// unbounded half. It sits under the inbound request deadline so the upstream
// call is what fails, and the runtime still has room to say so.
const upstreamClientTimeout = 45 * time.Second

// boundedUpstreamClient gives a caller-supplied client a timeout only when it
// declared none. A caller that set one has made a deliberate choice, and this
// must not quietly override it.
func boundedUpstreamClient(client *http.Client) *http.Client {
	if client == nil {
		return &http.Client{Timeout: upstreamClientTimeout}
	}
	if client.Timeout == 0 {
		bounded := *client
		bounded.Timeout = upstreamClientTimeout
		return &bounded
	}
	return client
}

type proxyBackend struct {
	mu         sync.Mutex
	endpoint   string
	httpClient *http.Client
	session    *mcp.ClientSession
	allowlist  []string
	baseline   map[string]string
	selected   []*mcp.Tool
	driftErr   error
	telemetry  *instrumentation
}

func newProxyBackend(ctx context.Context, upstreamURL string, allowTools []string, httpClient *http.Client, telemetry *instrumentation) (*proxyBackend, error) {
	if strings.TrimSpace(upstreamURL) == "" {
		return nil, fmt.Errorf("mcp-beaver: upstream endpoint is empty")
	}
	allowlist, err := ValidateAllowlist(allowTools)
	if err != nil {
		return nil, err
	}

	httpClient = boundedUpstreamClient(httpClient)

	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-beaver", Version: "0.1.0"}, nil)
	client.AddSendingMiddleware(telemetry.clientMiddleware)
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
		telemetry:  telemetry,
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
		return fmt.Errorf("mcp-beaver: tool %q is not allowlisted", name)
	}
	if fp != want {
		p.driftErr = fmt.Errorf("mcp-beaver: upstream schema drift for tool %q", name)
		return p.driftErr
	}
	if !slices.Contains(p.allowlist, name) {
		return fmt.Errorf("mcp-beaver: tool %q is not allowlisted", name)
	}
	return nil
}

func (p *proxyBackend) probeTools(ctx context.Context) (*mcp.ListToolsResult, error) {
	if strings.TrimSpace(p.endpoint) == "" {
		return nil, fmt.Errorf("mcp-beaver: upstream endpoint is empty")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-beaver-probe", Version: "0.1.0"}, nil)
	client.AddSendingMiddleware(p.telemetry.clientMiddleware)
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
	return nil, fmt.Errorf("mcp-beaver: upstream tool %q was not found or is no longer exposed", name)
}
