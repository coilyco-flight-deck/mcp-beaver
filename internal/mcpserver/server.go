package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/pkg/valuesource"
)

// defaultProtocolVersion is the MCP revision ward-mcp advertises when a client
// offers nothing we recognize. Known revisions are echoed back verbatim.
const defaultProtocolVersion = "2025-06-18"

// knownProtocolVersions are the MCP revisions ward-mcp will speak; a client
// asking for one gets it echoed, otherwise it is nudged to the default.
var knownProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
}

// Server is a parsed `.mcp.kdl` projected into MCP tools plus the opcore runtime
// that fires them. It is transport-agnostic: dispatch turns one JSON-RPC request
// into one response, and the HTTP/SSE transports move the bytes. See serve.go.
type Server struct {
	name    string
	tools   []tool            // projected tools, in a stable order
	byName  map[string]opTool // tool name -> descriptor + runtime binding
	runtime *opcore.Runtime
}

// tool is the MCP-envelope view of one grant: what tools/list emits.
type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// opTool binds a projected tool name to its opcore descriptor and the neutral
// input schema (whose Location hints route each argument onto path/query/body).
type opTool struct {
	desc   opcore.Descriptor
	schema opcore.Schema
}

// New parses a `.mcp.kdl` source and builds the server: one MCP tool per grant,
// an opcore.Runtime wired to the shared value providers (env, file, literal).
// It fails closed on any parse or projection error, exactly as opcore does.
func New(name string, src []byte) (*Server, error) {
	descs, cfg, err := opcore.ParseInline(src)
	if err != nil {
		return nil, err
	}
	// The KDL states no opaque values; the consumer supplies the providers that
	// resolve `value env "..."` at request time. Builtins cover env/file/literal.
	cfg.Providers = valuesource.Builtins()
	rt := opcore.NewRuntime(cfg)

	s := &Server{name: name, byName: map[string]opTool{}, runtime: rt}
	for _, d := range descs {
		name := toolName(d)
		if _, dup := s.byName[name]; dup {
			// Two grants projecting to the same tool name is unresolvable; fail
			// closed rather than silently shadow one.
			return nil, fmt.Errorf("ward-mcp: duplicate tool name %q from grant %q", name, d.Grant)
		}
		schema := d.InputSchema()
		s.tools = append(s.tools, tool{
			Name:        name,
			Description: describe(d),
			InputSchema: json.RawMessage(schema.JSONSchema()),
		})
		s.byName[name] = opTool{desc: d, schema: schema}
	}
	sort.Slice(s.tools, func(i, j int) bool { return s.tools[i].Name < s.tools[j].Name })
	return s, nil
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

// dispatch turns one inbound JSON-RPC request into its response, or nil when the
// message is a notification (which takes no reply). It is the single entry point
// both transports share.
func (s *Server) dispatch(ctx context.Context, req request) *response {
	if req.JSONRPC != jsonrpcVersion {
		if req.isNotification() {
			return nil
		}
		return errorResponse(req.ID, codeInvalidRequest, "jsonrpc must be "+jsonrpcVersion)
	}
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "notifications/initialized", "notifications/cancelled":
		return nil // notifications: acknowledged by the transport, no reply
	case "ping":
		return result(req.ID, map[string]any{})
	case "tools/list":
		return result(req.ID, map[string]any{"tools": s.tools})
	case "tools/call":
		return s.handleToolsCall(ctx, req)
	default:
		if req.isNotification() {
			return nil
		}
		return errorResponse(req.ID, codeMethodNotFound, "unknown method: "+req.Method)
	}
}

// handleInitialize answers the MCP handshake: echo a protocol version we speak
// (or nudge to the default), advertise the tools capability, and name ourselves.
func (s *Server) handleInitialize(req request) *response {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(req.Params, &params)
	proto := defaultProtocolVersion
	if knownProtocolVersions[params.ProtocolVersion] {
		proto = params.ProtocolVersion
	}
	return result(req.ID, map[string]any{
		"protocolVersion": proto,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": s.name, "version": "0.1.0"},
	})
}
