package mcpserver

import (
	"context"
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

// Server is a parsed `.mcp.kdl` projected into MCP tools plus the official MCP
// Go SDK runtime that serves them over transport/session plumbing.
type Server struct {
	name string
	sdk  *mcp.Server
}

// New parses a `.mcp.kdl` source and builds the SDK-backed server: one MCP tool
// per grant, with opcore still owning the guardfile parse, guard, and upstream
// request execution.
func New(name string, src []byte) (*Server, error) {
	descs, cfg, err := opcore.ParseInline(src)
	if err != nil {
		return nil, err
	}
	cfg.Providers = valuesource.Builtins()
	rt := opcore.NewRuntime(cfg)

	s := &Server{
		name: name,
		sdk:  mcp.NewServer(&mcp.Implementation{Name: name, Version: "0.1.0"}, nil),
	}

	seen := map[string]bool{}
	sort.Slice(descs, func(i, j int) bool { return toolName(descs[i]) < toolName(descs[j]) })
	for _, d := range descs {
		desc := d
		tname := toolName(desc)
		if seen[tname] {
			return nil, fmt.Errorf("ward-mcp: duplicate tool name %q from grant %q", tname, desc.Grant)
		}
		seen[tname] = true
		t := &mcp.Tool{
			Name:        tname,
			Description: describe(desc),
			InputSchema: json.RawMessage(desc.InputSchema().JSONSchema()),
		}
		s.sdk.AddTool(t, toolHandler(rt, desc))
	}
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

	return s, nil
}

// Handler exposes the runtime on /mcp using the official SDK streamable HTTP
// handler, plus the pod health probe.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s.sdk
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
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
