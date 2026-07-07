package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"forgejo.coilysiren.me/coilyco-flight-deck/cli-guard/http/opcore"
)

// toolCallParams is the params of a `tools/call` request: the tool name and its
// arguments object, keyed as the derived inputSchema declares.
type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// handleToolsCall runs one tool: split its arguments onto the opcore Args by the
// schema's Location hint, fire the self-guarding Operation.Execute, and render
// the response back as MCP tool content. An upstream/guard failure comes back as
// a tool result with isError set (not a JSON-RPC error), the MCP convention that
// keeps the model in the loop on a denied or failed call.
func (s *Server) handleToolsCall(ctx context.Context, req request) *response {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return errorResponse(req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
	}
	ot, ok := s.byName[p.Name]
	if !ok {
		// Deny-by-absence: a tool the guardfile never granted cannot be called.
		return errorResponse(req.ID, codeMethodNotFound, "unknown tool: "+p.Name)
	}

	args := splitArgs(ot.schema, p.Arguments)
	op := opcore.Operation{Desc: ot.desc, RT: s.runtime}
	resp, err := op.Execute(ctx, args)
	if err != nil {
		return result(req.ID, toolError(err))
	}
	return result(req.ID, toolSuccess(resp))
}

// splitArgs routes each supplied argument onto the opcore Args location its
// schema Property names: path and query values reach the URL (the injection
// surface opcore's metachar gate covers), body values do not. An argument with
// no matching property is dropped - the tool surface is exactly the schema.
func splitArgs(schema opcore.Schema, in map[string]any) opcore.Args {
	a := opcore.Args{
		Path:  map[string]string{},
		Query: map[string]string{},
		Body:  map[string]any{},
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
			a.Query[name] = scalarString(val)
		case opcore.LocationBody, opcore.LocationForm:
			a.Body[name] = val
		}
	}
	return a
}

// scalarString renders a path/query scalar as the string opcore fills into the
// URL. A string passes through; everything else takes fmt's default so a numeric
// or boolean path param still lands as text.
func scalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// toolSuccess renders a fired opcore.Response as MCP tool content: the raw JSON
// body as a single text block, plus structuredContent so a client that can read
// structured results gets the decoded value without re-parsing.
func toolSuccess(resp opcore.Response) map[string]any {
	text := string(resp.Raw)
	if text == "" {
		text = resp.Status
	}
	out := map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": false,
	}
	if resp.Decoded != nil {
		out["structuredContent"] = map[string]any{"result": resp.Decoded}
	}
	return out
}

// toolError renders a guard/upstream failure as an MCP tool result with isError
// set, so the calling model sees the denial as tool output rather than a
// transport fault. The engine's coded message carries the reason.
func toolError(err error) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": err.Error()}},
		"isError": true,
	}
}
