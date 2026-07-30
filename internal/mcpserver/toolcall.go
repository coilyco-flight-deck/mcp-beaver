package mcpserver

import (
	"fmt"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// splitArgs routes each supplied argument onto the opcore Args location its
// schema property names. Path and query values reach the URL, body values do
// not. An argument with no matching property is dropped - the tool surface is
// exactly the schema.
func splitArgs(schema opcore.Schema, in map[string]any) opcore.Args {
	a := opcore.Args{
		Path:        map[string]string{},
		Query:       map[string]string{},
		QueryValues: map[string]any{},
		Body:        map[string]any{},
	}
	for name, val := range in {
		prop, ok := schema.Properties[name]
		if !ok {
			continue
		}
		switch prop.Location {
		case opcore.LocationPath:
			a.Path[name] = scalarString(val)
		case opcore.LocationQuery:
			a.QueryValues[name] = val
		case opcore.LocationBody, opcore.LocationForm:
			a.Body[name] = val
		}
	}
	return a
}

// scalarString renders a path scalar as the string opcore fills into the URL.
// Query values retain their JSON types in Args.QueryValues so opcore can enforce
// typed scalar and repeated-array contracts before assembling the request.
func scalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func toolSuccess(resp opcore.Response) *mcp.CallToolResult {
	text := string(resp.Raw)
	if text == "" {
		text = resp.Status
	}
	result := any(resp.Decoded)
	if result == nil {
		result = text
	}
	out := &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: text}},
		StructuredContent: map[string]any{"result": result},
	}
	return out
}

func toolError(err error) *mcp.CallToolResult {
	out := &mcp.CallToolResult{}
	out.SetError(err)
	return out
}
