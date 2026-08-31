package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// confirmInputID names the single input request a confirmation raises. The
// client echoes it back in inputResponses on the retry.
const confirmInputID = "mcp-beaver/confirm"

// emptyElicitSchema is a schema with no properties. An empty-schema
// elicitation is the protocol's message-only approval prompt: clients render
// it as a confirm dialog rather than a form, which is exactly what a
// destructive tool wants. Asking for typed input here would invite a model to
// fill the form itself.
var emptyElicitSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// confirmConfig maps a projected tool name to its confirmation message.
type confirmConfig map[string]string

// parseConfirmations reads top-level `confirm` nodes, siblings of `wrap`:
//
//	confirm "create_thing"
//	confirm "delete_thing" message="This permanently deletes the issue. Continue?"
//
// The single argument is the PROJECTED TOOL NAME, not the grant, because that
// is the name the client sees and the name the runtime dispatches on.
//
// Opt-in per tool rather than automatic on every mutation. Confirmation is a
// behaviour change to a tool an agent already calls unattended, and mcp-beaver
// runs headless deployments where a blanket prompt would wedge every write.
// The annotations already advertise destructiveness for clients that want to
// decide for themselves; this is for the cases where the server insists.
func parseConfirmations(sources []guardSource) (confirmConfig, error) {
	nodes, err := parseInlineNodes(sources, "confirmations")
	if err != nil {
		return nil, err
	}
	out := confirmConfig{}
	for _, sn := range nodes {
		n := sn.node
		if n.Name() != "confirm" {
			continue
		}
		tool, err := oneStringArg(n, "confirm")
		if err != nil {
			return nil, err
		}
		if _, dup := out[tool]; dup {
			return nil, fmt.Errorf("mcp-beaver: duplicate `confirm` for tool %q", tool)
		}
		message := fmt.Sprintf("Run %q? This tool changes upstream state.", tool)
		for key, value := range n.Properties() {
			switch key {
			case "message":
				if value.String() == "" {
					return nil, fmt.Errorf("mcp-beaver: `confirm` %q message must be non-empty", tool)
				}
				message = value.String()
			default:
				return nil, fmt.Errorf("mcp-beaver: unknown `confirm` property %q (want message; fail-closed)", key)
			}
		}
		out[tool] = message
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// validateConfirmations rejects a `confirm` naming a tool the spec does not
// mint. A confirmation silently attached to nothing is the dangerous failure:
// the author believes a destructive tool is gated when it is not.
func validateConfirmations(cfg confirmConfig, tools []*mcp.Tool) error {
	if cfg == nil {
		return nil
	}
	served := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool != nil {
			served[tool.Name] = true
		}
	}
	for name := range cfg {
		if !served[name] {
			return fmt.Errorf("mcp-beaver: `confirm` names tool %q, which this spec does not mint", name)
		}
	}
	return nil
}

// withConfirmation wraps a tool handler in the Multi Round-Trip Request
// pattern: the first call returns an input request instead of acting, and the
// client retries carrying the user's answer. The SDK bridges older clients by
// issuing the elicitation itself and re-invoking this handler with the
// response filled in, so one code path covers both protocol eras.
func withConfirmation(message string, next mcp.ToolHandler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		answer, ok := req.Params.InputResponses[confirmInputID]
		if !ok {
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{
					confirmInputID: &mcp.ElicitParams{
						Message:         message,
						RequestedSchema: emptyElicitSchema,
					},
				},
			}, nil
		}
		elicited, isElicit := answer.(*mcp.ElicitResult)
		if !isElicit || elicited.Action != "accept" {
			// Anything other than an explicit accept is a refusal. A declined
			// or dismissed prompt must not fall through to the upstream call.
			return toolError(fmt.Errorf("mcp-beaver: confirmation was not accepted, tool not run")), nil
		}
		return next(ctx, req)
	}
}
