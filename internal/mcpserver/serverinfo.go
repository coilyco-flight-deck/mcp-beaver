package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultServerInfoTool = "ward_mcp_info"

var serverInfoInputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{},
	"additionalProperties":false
}`)

var serverInfoOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"status":{"type":"string"},
		"server":{"type":"string"},
		"spec":{"type":"string"},
		"mode":{"type":"string"},
		"toolCount":{"type":"integer"},
		"tools":{"type":"array","items":{"type":"string"}},
		"resourceCount":{"type":"integer"},
		"promptCount":{"type":"integer"}
	},
	"required":["status","server","mode","toolCount","tools"],
	"additionalProperties":false
}`)

// serverInfoConfig is the parsed `server-info` node, or nil when the guardfile
// does not declare one.
type serverInfoConfig struct {
	toolName string
}

// parseServerInfo reads the optional top-level `server-info` node:
//
//	server-info                       // mints `ward_mcp_info`
//	server-info name="status"         // mints `status` instead
//
// Opt-in on purpose. ward-mcp's security model is deny-by-absence: the served
// surface is exactly what the guardfile grants. A tool minted into every server
// regardless of its spec would be the first exception to that, and a
// locked-down deployment has a legitimate reason to refuse to describe its own
// shape. Absent node, no tool.
//
// It also fills a real gap rather than being decoration: 2026-07-28 removed
// the protocol-level `ping`, so a client has no built-in liveness probe left.
func parseServerInfo(src []byte) (*serverInfoConfig, error) {
	doc, err := parseInlineDoc(src, "server-info")
	if err != nil {
		return nil, err
	}
	var cfg *serverInfoConfig
	for _, n := range doc.Nodes {
		if n.Name() != "server-info" {
			continue
		}
		if cfg != nil {
			return nil, fmt.Errorf("ward-mcp: duplicate `server-info` node")
		}
		if len(n.Arguments()) != 0 {
			return nil, fmt.Errorf("ward-mcp: `server-info` takes no arguments, only an optional name= property")
		}
		out := &serverInfoConfig{toolName: defaultServerInfoTool}
		for key, value := range n.Properties() {
			switch key {
			case "name":
				if value.String() == "" {
					return nil, fmt.Errorf("ward-mcp: `server-info` name must be non-empty")
				}
				out.toolName = value.String()
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `server-info` property %q (want name; fail-closed)", key)
			}
		}
		cfg = out
	}
	return cfg, nil
}

// serverInfoTool builds the tool spec. It is built before telemetry so the
// info tool is part of the bounded metric label set like any granted tool,
// rather than reporting itself as _OTHER, and part of s.tools so `lint` and
// ToolNames report the surface the runtime actually serves.
func serverInfoTool(cfg *serverInfoConfig, granted []*mcp.Tool) (*mcp.Tool, error) {
	if cfg == nil {
		return nil, nil
	}
	for _, tool := range granted {
		if tool != nil && tool.Name == cfg.toolName {
			return nil, fmt.Errorf("ward-mcp: `server-info` tool name %q collides with a granted tool", cfg.toolName)
		}
	}
	readOnly, destructive, openWorld := true, false, false
	return &mcp.Tool{
		Name:         cfg.toolName,
		Title:        "Server info",
		Description:  "Report this MCP server's identity, mode, and served tool inventory. Read-only, reaches no upstream, and doubles as a liveness probe now that the protocol ping is gone.",
		InputSchema:  serverInfoInputSchema,
		OutputSchema: serverInfoOutputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &openWorld,
		},
	}, nil
}

// registerServerInfo wires the handler for a tool serverInfoTool already built.
// The payload reports the server's own shape and nothing about the upstream
// beyond whether one is proxied: it must not become a way to read
// configuration the grants themselves do not expose.
func (s *Server) registerServerInfo(tool *mcp.Tool) {
	if tool == nil {
		return
	}
	s.registerTool(tool, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		payload := s.serverInfoPayload()
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
			StructuredContent: payload,
		}, nil
	})
}

func (s *Server) serverInfoPayload() map[string]any {
	mode := "spec"
	if len(s.upstreams) > 0 {
		mode = "upstream"
	}
	payload := map[string]any{
		"status":        "ok",
		"server":        s.name,
		"mode":          mode,
		"toolCount":     len(s.tools),
		"tools":         projectedToolNames(s.tools),
		"resourceCount": len(s.resources),
		"promptCount":   len(s.prompts),
	}
	if s.specPath != "" {
		payload["spec"] = s.specName()
	}
	return payload
}
