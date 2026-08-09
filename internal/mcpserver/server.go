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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

const serverInstructions = "This server exposes only policy-approved tools. Use read-only tools to inspect state before mutation tools. Follow each tool's input and output schema, and treat safety annotations as hints rather than authorization."

var resultOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{"result":{}},
	"required":["result"],
	"additionalProperties":false
}`)

// Server is a guarded MCP runtime backed by either local opcore grants or an
// allowlisted upstream streamable-HTTP MCP server.
type Server struct {
	name      string
	specPath  string
	descs     []opcore.Descriptor
	cfg       opcore.RuntimeConfig
	tools     []*mcp.Tool
	handlers  map[string]mcp.ToolHandler
	upstreams []adminUpstreamResponse
	sdk       *mcp.Server
	telemetry *instrumentation
	closeFn   func() error
}

// New parses a `.mcp.kdl` source and builds the SDK-backed server: one MCP tool
// and matching HTTP endpoint per grant, with opcore still owning the guardfile
// parse, guard, and upstream request execution.
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
	instrumentation, err := newInstrumentation("spec", tools)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}

	// Top-level `icon` nodes ride beside `wrap`, outside the frozen inline
	// grammar opcore owns (deploy#255) - parsed here, served on initialize.
	icons, err := parseIcons(src)
	if err != nil {
		return nil, err
	}

	s := &Server{
		name:      name,
		specPath:  specPath,
		descs:     descs,
		cfg:       cfg,
		tools:     tools,
		handlers:  make(map[string]mcp.ToolHandler, len(descs)),
		sdk:       newSDKServer(name, icons),
		telemetry: instrumentation,
	}

	for _, d := range descs {
		desc := d
		s.registerTool(toolSpec(desc), toolHandler(rt, desc))
	}
	s.installMiddleware()
	return s, nil
}

// NewProxy connects to an upstream streamable-HTTP MCP server and exposes only
// the selected upstream tools. The outward contract preserves the upstream tool
// schemas, descriptions, titles, and annotations where possible.
func NewProxy(ctx context.Context, name, specPath, upstreamURL string, allowTools []string, httpClient *http.Client) (*Server, error) {
	declaredTools := make([]*mcp.Tool, 0, len(allowTools))
	for _, tool := range allowTools {
		declaredTools = append(declaredTools, &mcp.Tool{Name: tool})
	}
	instrumentation, err := newInstrumentation("upstream", declaredTools)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}
	proxy, err := newProxyBackend(ctx, upstreamURL, allowTools, httpClient, instrumentation)
	if err != nil {
		return nil, err
	}

	s := &Server{
		name:      name,
		specPath:  specPath,
		tools:     proxy.selectedTools(),
		handlers:  make(map[string]mcp.ToolHandler, len(allowTools)),
		upstreams: []adminUpstreamResponse{{Kind: "mcp", Mode: "streamable-http"}},
		sdk:       newSDKServer(name, nil),
		telemetry: instrumentation,
		closeFn:   proxy.Close,
	}
	for _, tool := range proxy.selectedTools() {
		t := cloneTool(tool)
		s.registerTool(t, proxy.toolHandler(tool.Name))
	}
	s.installMiddleware()
	return s, nil
}

// ToolNames returns the projected tool names in the order the runtime serves
// them. `ward-mcp lint` prints these so a consumer can read the minted surface
// off the owning loader instead of writing a second parser for the same file.
func (s *Server) ToolNames() []string {
	return projectedToolNames(s.tools)
}

// NotReadOnly returns the served tool names the upstream does not annotate
// `readOnlyHint: true`, sorted. A tool with no annotations counts, since the
// MCP default for the hint is false: an upstream that stays silent has not
// promised anything, and a read-only allowlist must not assume one.
func (s *Server) NotReadOnly() []string {
	var out []string
	for _, tool := range s.tools {
		if tool == nil {
			continue
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			out = append(out, tool.Name)
		}
	}
	sort.Strings(out)
	return out
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
// handler, automatically projects each tool at /api/{tool-name}, and retains
// the pod health probe plus operator admin endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.sdk
	}, &mcp.StreamableHTTPOptions{JSONResponse: true})
	mux.Handle("/mcp", captureTransportSpan(mcpHandler))
	mux.HandleFunc(apiPrefix, s.serveAPITool)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc(adminDescribePath, s.serveAdminDescribe)
	mux.HandleFunc(adminReloadPath, s.serveAdminReload)
	return otelhttp.NewHandler(mux, "ward-mcp HTTP",
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/healthz" }),
	)
}

func captureTransportSpan(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cloned := r.Clone(r.Context())
		cloned.Header = r.Header.Clone()
		cloned.Header.Del(transportTraceparentHeader)
		spanContext := trace.SpanContextFromContext(r.Context())
		if !spanContext.IsValid() {
			next.ServeHTTP(w, cloned)
			return
		}
		cloned.Header.Set(transportTraceparentHeader, fmt.Sprintf(
			"00-%s-%s-%02x",
			spanContext.TraceID(), spanContext.SpanID(), byte(spanContext.TraceFlags()),
		))
		next.ServeHTTP(w, cloned)
	})
}

func (s *Server) installMiddleware() {
	s.sdk.AddReceivingMiddleware(s.telemetry.serverMiddleware, func(next mcp.MethodHandler) mcp.MethodHandler {
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

func (s *Server) registerTool(tool *mcp.Tool, handler mcp.ToolHandler) {
	handler = s.telemetry.toolHandler(tool.Name, handler)
	s.handlers[tool.Name] = handler
	s.sdk.AddTool(tool, handler)
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

func newSDKServer(name string, icons []mcp.Icon) *mcp.Server {
	return mcp.NewServer(
		&mcp.Implementation{Name: name, Version: "0.1.0", Icons: icons},
		&mcp.ServerOptions{Instructions: serverInstructions},
	)
}

func toolSpec(d opcore.Descriptor) *mcp.Tool {
	return &mcp.Tool{
		Name:         toolName(d),
		Title:        toolTitle(d),
		Description:  describe(d),
		InputSchema:  json.RawMessage(d.InputSchema().JSONSchema()),
		OutputSchema: resultOutputSchema,
		Annotations:  toolAnnotations(d),
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

func toolTitle(d opcore.Descriptor) string {
	title := strings.NewReplacer("_", " ", "-", " ").Replace(toolName(d))
	if title == "" {
		return ""
	}
	return strings.ToUpper(title[:1]) + title[1:]
}

// describe is the tool's human description: the Guardfile `describe "..."` note
// when present, else a user-goal sentence derived from the authorizing grant.
func describe(d opcore.Descriptor) string {
	if strings.TrimSpace(d.Describe) != "" {
		return d.Describe
	}
	return fmt.Sprintf("Use this when the user wants to %s %s through the configured upstream service.", d.Leaf, d.Group)
}

func toolAnnotations(d opcore.Descriptor) *mcp.ToolAnnotations {
	destructive := d.Destructive
	openWorld := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    isReadOnlyMethod(d.Method),
		DestructiveHint: &destructive,
		IdempotentHint:  isIdempotentMethod(d.Method),
		OpenWorldHint:   &openWorld,
	}
}

func isReadOnlyMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func isIdempotentMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
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
