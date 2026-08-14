package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	kdl "github.com/calico32/kdl-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// withheldMetaKey marks a stub in the tool's `_meta`, so a client can tell a
// stub from a live tool without parsing prose out of the description. Prose is
// for the model; this is for the client.
const withheldMetaKey = "coilyco.io/withheld"

// withheldErrorCode is the machine-readable class in the refusal payload. It
// distinguishes "policy withheld this" from every other way a call can fail,
// which is the whole distinction #54 is about.
const withheldErrorCode = "verb_withheld"

var withheldInputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{},
	"additionalProperties":false
}`)

var withheldOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"error":{"type":"string"},
		"reason":{"type":"string"},
		"alternative":{"type":"string"}
	},
	"required":["error","reason"],
	"additionalProperties":false
}`)

// withheldStub is one deliberately-omitted verb, stated so it can be seen.
type withheldStub struct {
	tool        string
	reason      string
	alternative string
}

// parseWithheld reads top-level `withhold` nodes, siblings of `wrap`:
//
//	withhold "edit_issue-comment" {
//	    reason "Comment edits are withheld here for audit-trail integrity."
//	    alternative "comment_issue"
//	}
//
// The argument is the PROJECTED TOOL NAME the stub occupies, matching
// `confirm`, because that is the name a client sees and dispatches on.
//
// The problem this solves is that absence carries four meanings at once -
// withheld by policy, not yet implemented, not offered upstream, or simply not
// matched by the agent's search - and an agent reasoning from a hole in the
// tool list has to guess which. The guesses go wrong in both directions: a
// real capability gets worked around because it looked absent, or a workaround
// gets built for a restriction that was never there.
//
// A stub is not a weakening of deny-by-absence. It reaches no upstream, holds
// no credential, and refuses every call. It converts silence into a statement.
func parseWithheld(src []byte) ([]withheldStub, error) {
	doc, err := parseInlineDoc(src, "withhold")
	if err != nil {
		return nil, err
	}
	var out []withheldStub
	seen := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Name() != "withhold" {
			continue
		}
		tool, err := oneStringArg(n, "withhold")
		if err != nil {
			return nil, err
		}
		if seen[tool] {
			return nil, fmt.Errorf("ward-mcp: duplicate `withhold` for tool %q", tool)
		}
		seen[tool] = true
		stub, err := withheldChildren(n, tool)
		if err != nil {
			return nil, err
		}
		out = append(out, stub)
	}
	return out, nil
}

func withheldChildren(n *kdl.Node, tool string) (withheldStub, error) {
	stub := withheldStub{tool: tool}
	if len(n.Properties()) > 0 {
		return stub, fmt.Errorf("ward-mcp: `withhold` %q takes no properties, only `reason` and `alternative` children (fail-closed)", tool)
	}
	for _, child := range n.Children().Nodes {
		value, err := oneStringArg(child, child.Name())
		if err != nil {
			return stub, err
		}
		switch child.Name() {
		case "reason":
			if stub.reason != "" {
				return stub, fmt.Errorf("ward-mcp: `withhold` %q has a duplicate `reason`", tool)
			}
			stub.reason = value
		case "alternative":
			if stub.alternative != "" {
				return stub, fmt.Errorf("ward-mcp: `withhold` %q has a duplicate `alternative`", tool)
			}
			stub.alternative = value
		default:
			return stub, fmt.Errorf("ward-mcp: unknown `withhold` child %q (want reason | alternative; fail-closed)", child.Name())
		}
	}
	// Required, because a stub that does not say why is the silence it was
	// meant to replace, only louder.
	if stub.reason == "" {
		return stub, fmt.Errorf("ward-mcp: `withhold` %q needs a `reason`: a stub with no stated reason restates the absence it replaces", tool)
	}
	return stub, nil
}

// withheldTools builds one stub tool per declared node, after validating each
// against the surface the spec actually mints.
func withheldTools(stubs []withheldStub, granted []*mcp.Tool) ([]*mcp.Tool, error) {
	if len(stubs) == 0 {
		return nil, nil
	}
	served := make(map[string]bool, len(granted))
	for _, tool := range granted {
		if tool != nil {
			served[tool.Name] = true
		}
	}
	out := make([]*mcp.Tool, 0, len(stubs))
	for _, stub := range stubs {
		// A stub shadowing a live tool is the one genuinely dangerous case:
		// the surface would advertise a working capability as refused, and
		// the grant would look revoked without anything revoking it.
		if served[stub.tool] {
			return nil, fmt.Errorf("ward-mcp: `withhold` names tool %q, which this spec mints: remove the grant or the stub, not both", stub.tool)
		}
		// A named alternative that does not exist sends an agent hunting for
		// a tool it will never find, which is the second failure in #54.
		if stub.alternative != "" && !served[stub.alternative] {
			return nil, fmt.Errorf("ward-mcp: `withhold` %q names alternative %q, which this spec does not mint", stub.tool, stub.alternative)
		}
		out = append(out, withheldTool(stub))
	}
	return out, nil
}

func withheldTool(stub withheldStub) *mcp.Tool {
	readOnly, destructive, openWorld := true, false, false
	tool := &mcp.Tool{
		Name:         stub.tool,
		Title:        "Withheld: " + stub.tool,
		Description:  withheldDescription(stub),
		InputSchema:  withheldInputSchema,
		OutputSchema: withheldOutputSchema,
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  true,
			OpenWorldHint:   &openWorld,
		},
	}
	meta := map[string]any{withheldMetaKey: true}
	if stub.alternative != "" {
		meta[withheldMetaKey+"/alternative"] = stub.alternative
	}
	tool.SetMeta(meta)
	return tool
}

// withheldDescription leads with the refusal so a model reading only the first
// clause still rules the tool out.
func withheldDescription(stub withheldStub) string {
	var b strings.Builder
	b.WriteString("NOT AVAILABLE - this operation is intentionally withheld on this surface. ")
	b.WriteString(stub.reason)
	if stub.alternative != "" {
		b.WriteString(" Use ")
		b.WriteString(stub.alternative)
		b.WriteString(" instead.")
	}
	b.WriteString(" Calling this tool always fails and reaches no upstream.")
	return b.String()
}

// registerWithheld wires the refusing handlers. The result is an error result
// rather than a success carrying a refusal: the call did not do the thing, and
// a model that only checks isError must not read it as having worked.
func (s *Server) registerWithheld(stubs []withheldStub, tools []*mcp.Tool) {
	byName := make(map[string]withheldStub, len(stubs))
	for _, stub := range stubs {
		byName[stub.tool] = stub
	}
	for _, tool := range tools {
		stub := byName[tool.Name]
		s.registerTool(tool, withheldHandler(stub))
	}
}

func withheldHandler(stub withheldStub) mcp.ToolHandler {
	payload := map[string]any{
		"error":  withheldErrorCode,
		"reason": stub.reason,
	}
	if stub.alternative != "" {
		payload["alternative"] = stub.alternative
	}
	return func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		out := &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
			StructuredContent: payload,
			IsError:           true,
		}
		return out, nil
	}
}
