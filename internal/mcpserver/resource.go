package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// inlineResource is one `resource` node: static content the server serves at a
// URI. Claude Code surfaces these as `@` mentions.
type inlineResource struct {
	tool mcp.Resource
	body string
}

// parseResources reads top-level `resource` nodes, siblings of `wrap` in the
// same position as `icon`:
//
//	resource "oncall-runbook" uri="ward://runbook/oncall" mime="text/markdown" {
//	    description "What to check first when the upstream 5xxes"
//	    text "1. Check /healthz on the pod."
//	    text "2. Check the upstream's own status page."
//	}
//
// The single argument is the resource name. `uri` is required and is what a
// client reads back. Content is INLINE ONLY, by design: a resource that
// proxied an upstream read would be a second, unguarded egress path beside the
// `can` grants, and the grants are the whole security model. Static content
// adds a surface to read without adding a surface to reach.
func parseResources(src []byte) ([]inlineResource, error) {
	doc, err := parseInlineDoc(src, "resources")
	if err != nil {
		return nil, err
	}
	var out []inlineResource
	seen := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Name() != "resource" {
			continue
		}
		name, err := oneStringArg(n, "resource")
		if err != nil {
			return nil, err
		}
		res := mcp.Resource{Name: name}
		for key, value := range n.Properties() {
			switch key {
			case "uri":
				res.URI = value.String()
			case "mime":
				res.MIMEType = value.String()
			case "title":
				res.Title = value.String()
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `resource` property %q (want uri | mime | title; fail-closed)", key)
			}
		}
		if res.URI == "" {
			return nil, fmt.Errorf("ward-mcp: `resource` %q needs a non-empty uri", name)
		}
		if seen[res.URI] {
			return nil, fmt.Errorf("ward-mcp: duplicate `resource` uri %q", res.URI)
		}
		seen[res.URI] = true
		for _, child := range n.Children().Nodes {
			if child.Name() == "description" {
				if len(child.Arguments()) != 1 {
					return nil, fmt.Errorf("ward-mcp: `resource` %q child `description` wants exactly one argument", name)
				}
				res.Description = child.Arg(0).String()
			}
		}
		body, err := joinTextChildren(n, "resource")
		if err != nil {
			return nil, err
		}
		if body == "" {
			return nil, fmt.Errorf("ward-mcp: `resource` %q has no `text` content", name)
		}
		if res.MIMEType == "" {
			res.MIMEType = "text/plain"
		}
		out = append(out, inlineResource{tool: res, body: body})
	}
	return out, nil
}

// registerResources adds each parsed resource to the SDK server. Registering
// any resource is what makes the SDK infer the `resources` capability, so a
// spec with no `resource` node advertises none.
func (s *Server) registerResources(resources []inlineResource) {
	for _, r := range resources {
		res := r
		s.resources = append(s.resources, res.tool)
		s.sdk.AddResource(&res.tool, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      res.tool.URI,
					MIMEType: res.tool.MIMEType,
					Text:     res.body,
				}},
			}, nil
		})
	}
}
