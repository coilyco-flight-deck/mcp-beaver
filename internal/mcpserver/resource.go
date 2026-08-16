package mcpserver

import (
	"context"
	"fmt"

	kdl "github.com/calico32/kdl-go"
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
//	resource "oncall-runbook" uri="ward://runbook/oncall" mime="text/markdown" priority=0.9 {
//	    audience "assistant"
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
//
// `audience` and `priority` are the MCP annotations a host reads to decide
// whether to pull a resource into a model's context on its own. A host that
// gates on `audience` sees an unannotated resource as not meant for the model
// and skips it, so serving reference material to an agent needs the annotation
// stated here rather than assumed downstream.
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
			case "priority":
				priority, err := floatProp(value, "resource", key)
				if err != nil {
					return nil, err
				}
				if priority < 0 || priority > 1 {
					return nil, fmt.Errorf("ward-mcp: `resource` %q priority %v is outside 0..1", name, priority)
				}
				resourceAnnotations(&res).Priority = priority
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `resource` property %q (want uri | mime | title | priority; fail-closed)", key)
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
			switch child.Name() {
			case "description":
				if len(child.Arguments()) != 1 {
					return nil, fmt.Errorf("ward-mcp: `resource` %q child `description` wants exactly one argument", name)
				}
				res.Description = child.Arg(0).String()
			case "audience":
				roles, err := audienceRoles(child, name)
				if err != nil {
					return nil, err
				}
				resourceAnnotations(&res).Audience = roles
			case "text":
				// Collected by joinTextChildren below.
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `resource` child %q on %q (want description | audience | text; fail-closed)", child.Name(), name)
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

// resourceAnnotations lazily allocates, so a resource declaring neither
// annotation keeps a nil Annotations and serializes without the field.
func resourceAnnotations(res *mcp.Resource) *mcp.Annotations {
	if res.Annotations == nil {
		res.Annotations = &mcp.Annotations{}
	}
	return res.Annotations
}

// audienceRoles reads `audience "assistant" "user"`. The spec defines exactly
// these two roles, and a host matches on them literally, so an unrecognised
// role fails closed. Accepting one would produce a resource that lists an
// audience and is still skipped by every host, which is the silent failure
// this node exists to remove rather than reproduce.
func audienceRoles(n *kdl.Node, resourceName string) ([]mcp.Role, error) {
	if len(n.Arguments()) == 0 {
		return nil, fmt.Errorf("ward-mcp: `resource` %q child `audience` wants at least one role", resourceName)
	}
	roles := make([]mcp.Role, 0, len(n.Arguments()))
	seen := map[mcp.Role]bool{}
	for i := range n.Arguments() {
		role := mcp.Role(n.Arg(i).String())
		if role != "assistant" && role != "user" {
			return nil, fmt.Errorf("ward-mcp: `resource` %q audience role %q is not assistant or user (fail-closed)", resourceName, role)
		}
		if seen[role] {
			return nil, fmt.Errorf("ward-mcp: `resource` %q repeats audience role %q", resourceName, role)
		}
		seen[role] = true
		roles = append(roles, role)
	}
	return roles, nil
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
