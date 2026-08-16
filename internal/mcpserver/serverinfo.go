package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	kdl "github.com/calico32/kdl-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const defaultServerInfoTool = "mcp_beaver_info"

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

// serverInfoDisabledArg is the one accepted `server-info` argument, the
// explicit opt-out for a deployment that wants the tool gone.
const serverInfoDisabledArg = "disabled"

// serverInfoConfig is the resolved `server-info` state, or nil when the
// guardfile opts out.
type serverInfoConfig struct {
	toolName string
}

// parseServerInfo reads the optional top-level `server-info` node:
//
//	(no node)                         // mints `mcp_beaver_info` - the default
//	server-info name="status"         // mints `status` instead
//	server-info disabled              // mints nothing
//
// On by default. This is the one documented exception to deny-by-absence, and
// it is narrow enough to be worth the words it costs to explain. Every field
// the payload returns is already obtainable by any caller who can reach the
// endpoint at all - `server` from `initialize`, `tools` from `tools/list`,
// the counts from `resources/list` and `prompts/list` - so opting out
// withholds no information and only removes a convenience. Deny-by-absence
// still governs everything that reaches an upstream, which is every grant.
//
// It also fills a real gap rather than being decoration: 2026-07-28 removed
// the protocol-level `ping`, so a client has no built-in liveness probe left,
// and an inventory reported BY the server is the grounded answer to an agent
// describing its own capabilities. Both only pay off if it is reliably there:
// present-on-some-servers is worse than either extreme, because absence then
// carries no meaning an agent can read.
func parseServerInfo(src []byte) (*serverInfoConfig, error) {
	doc, err := parseInlineDoc(src, "server-info")
	if err != nil {
		return nil, err
	}
	cfg := &serverInfoConfig{toolName: defaultServerInfoTool}
	seen := false
	for _, n := range doc.Nodes {
		if n.Name() != "server-info" {
			continue
		}
		if seen {
			return nil, fmt.Errorf("mcp-beaver: duplicate `server-info` node")
		}
		seen = true
		disabled, err := serverInfoDisabled(n)
		if err != nil {
			return nil, err
		}
		for key, value := range n.Properties() {
			switch key {
			case "name":
				if value.String() == "" {
					return nil, fmt.Errorf("mcp-beaver: `server-info` name must be non-empty")
				}
				cfg.toolName = value.String()
			default:
				return nil, fmt.Errorf("mcp-beaver: unknown `server-info` property %q (want name; fail-closed)", key)
			}
		}
		if disabled {
			if len(n.Properties()) > 0 {
				return nil, fmt.Errorf("mcp-beaver: `server-info disabled` takes no properties: naming a tool that is not minted reads as a live override")
			}
			return nil, nil
		}
	}
	return cfg, nil
}

// serverInfoDisabled reads the opt-out argument, failing closed on any other
// argument so a typo suppresses nothing silently.
func serverInfoDisabled(n *kdl.Node) (bool, error) {
	switch len(n.Arguments()) {
	case 0:
		return false, nil
	case 1:
		if arg := n.Arg(0).String(); arg != serverInfoDisabledArg {
			return false, fmt.Errorf("mcp-beaver: unknown `server-info` argument %q (want %s, or no argument; fail-closed)", arg, serverInfoDisabledArg)
		}
		return true, nil
	default:
		return false, fmt.Errorf("mcp-beaver: `server-info` takes at most one argument (%s), plus an optional name= property", serverInfoDisabledArg)
	}
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
			// Reachable without a `server-info` node now that the tool is on
			// by default, so the message has to carry both migrations rather
			// than assuming the author opted in and can just back it out.
			return nil, fmt.Errorf(
				"mcp-beaver: `server-info` tool name %q collides with a granted tool: rename it with `server-info name=\"...\"`, or state `server-info %s`",
				cfg.toolName, serverInfoDisabledArg,
			)
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
