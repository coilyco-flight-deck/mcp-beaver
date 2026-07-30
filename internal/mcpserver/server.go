package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server is a guarded MCP runtime backed by either local opcore grants or an
// allowlisted upstream streamable-HTTP MCP server.
type Server struct {
	name      string
	specPath  string
	descs     []opcore.Descriptor
	cfg       opcore.RuntimeConfig
	tools     []*mcp.Tool
	upstreams []adminUpstreamResponse
	sdk       *mcp.Server
	closeFn   func() error
}

// New parses a `.mcp.kdl` source and builds the SDK-backed server: one MCP tool
// per grant, with opcore still owning the guardfile parse, guard, and upstream
// request execution.
func New(name, specPath string, src []byte) (*Server, error) {
	descs, cfg, err := opcore.ParseInline(src)
	if err != nil {
		return nil, err
	}
	cfg.Providers = valuesource.Builtins()
	rt := opcore.NewRuntime(cfg)

	tools, err := localTools(descs)
	if err != nil {
		return nil, err
	}

	// Top-level `icon` nodes ride beside `wrap`, outside the frozen inline
	// grammar opcore owns (deploy#255) - parsed here, served on initialize.
	icons, err := parseIcons(src)
	if err != nil {
		return nil, err
	}

	s := &Server{
		name:     name,
		specPath: specPath,
		descs:    descs,
		cfg:      cfg,
		tools:    tools,
		sdk:      mcp.NewServer(&mcp.Implementation{Name: name, Version: "0.1.0", Icons: icons}, nil),
	}

	for _, d := range descs {
		desc := d
		s.sdk.AddTool(toolSpec(desc), toolHandler(rt, desc))
	}
	s.installCallRewrite()
	return s, nil
}

// NewProxy connects to an upstream streamable-HTTP MCP server and exposes only
// the selected upstream tools. The outward contract preserves the upstream tool
// schemas, descriptions, titles, and annotations where possible.
func NewProxy(ctx context.Context, name, specPath, upstreamURL string, allowTools []string, httpClient *http.Client) (*Server, error) {
	proxy, err := newProxyBackend(ctx, upstreamURL, allowTools, httpClient)
	if err != nil {
		return nil, err
	}

	s := &Server{
		name:      name,
		specPath:  specPath,
		tools:     proxy.selectedTools(),
		upstreams: []adminUpstreamResponse{{Kind: "mcp", Mode: "streamable-http"}},
		sdk:       mcp.NewServer(&mcp.Implementation{Name: name, Version: "0.1.0"}, nil),
		closeFn:   proxy.Close,
	}
	for _, tool := range proxy.selectedTools() {
		t := cloneTool(tool)
		s.sdk.AddTool(t, proxy.toolHandler(tool.Name))
	}
	s.installCallRewrite()
	return s, nil
}

// Close releases any upstream session resources. Local opcore-backed servers do
// not hold additional runtime state, so Close is a no-op there.
func (s *Server) Close() error {
	if s.closeFn == nil {
		return nil
	}
	return s.closeFn()
}

// Handler exposes the runtime on /mcp using the official SDK streamable HTTP
// handler, plus the pod health probe and the operator admin endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.sdk
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc(adminDescribePath, s.serveAdminDescribe)
	mux.HandleFunc(adminReloadPath, s.serveAdminReload)
	return mux
}

func (s *Server) installCallRewrite() {
	s.sdk.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if method == "tools/call" {
				if jerr, ok := err.(*jsonrpc.Error); ok && jerr.Code == jsonrpc.CodeInvalidParams {
					if strings.HasPrefix(jerr.Message, "unknown tool") {
						return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: jerr.Message, Data: jerr.Data}
					}
				}
			}
			return res, err
		}
	})
}

func localTools(descs []opcore.Descriptor) ([]*mcp.Tool, error) {
	out := make([]*mcp.Tool, 0, len(descs))
	seen := map[string]bool{}
	sort.Slice(descs, func(i, j int) bool { return toolName(descs[i]) < toolName(descs[j]) })
	for _, d := range descs {
		tname := toolName(d)
		if seen[tname] {
			return nil, fmt.Errorf("ward-mcp: duplicate tool name %q from grant %q", tname, d.Grant)
		}
		seen[tname] = true
		out = append(out, toolSpec(d))
	}
	return out, nil
}

func toolSpecFromUpstream(tool *mcp.Tool) *mcp.Tool {
	if tool == nil {
		return nil
	}
	return cloneTool(tool)
}

func cloneTool(tool *mcp.Tool) *mcp.Tool {
	if tool == nil {
		return nil
	}
	cloned := *tool
	return &cloned
}

func toolSpec(d opcore.Descriptor) *mcp.Tool {
	return &mcp.Tool{
		Name:        toolName(d),
		Description: describe(d),
		InputSchema: json.RawMessage(d.InputSchema().JSONSchema()),
	}
}

func toolHandler(rt *opcore.Runtime, desc opcore.Descriptor) mcp.ToolHandler {
	schema := desc.InputSchema()
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var rawArgs map[string]any
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &rawArgs); err != nil {
				return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
			}
		}
		args := splitArgs(schema, rawArgs)
		resp, err := (&opcore.Operation{Desc: desc, RT: rt}).Execute(ctx, args)
		if err != nil {
			return toolError(err), nil
		}
		return toolSuccess(resp), nil
	}
}

// toolName projects a descriptor onto its MCP tool name: `verb_resource`, e.g.
// `create_issue`. Leaf is the verb, Group the resource. See DESIGN.md.
func toolName(d opcore.Descriptor) string {
	return d.Leaf + "_" + d.Group
}

// describe is the tool's human description: the Guardfile `describe "..."` note
// when present, else the authorizing grant sentence (`can create issue`), which
// is always meaningful and audit-anchored.
func describe(d opcore.Descriptor) string {
	if strings.TrimSpace(d.Describe) != "" {
		return d.Describe
	}
	return d.Grant
}

func (s *Server) specName() string {
	if s.specPath == "" {
		return s.name
	}
	base := s.specPath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".kdl")
	base = strings.TrimSuffix(base, ".mcp")
	if base == "" {
		return s.name
	}
	return base
}

// fingerprintTool serializes the upstream tool contract into a stable digest.
func fingerprintTool(tool *mcp.Tool) (string, error) {
	raw, err := json.Marshal(tool)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
