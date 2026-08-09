package mcpserver

import (
	"context"
	"fmt"
	"strings"

	kdl "github.com/calico32/kdl-go"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// inlinePrompt is one `prompt` node: a named, argument-taking message template.
// Claude Code surfaces these as slash commands.
type inlinePrompt struct {
	prompt mcp.Prompt
	body   string
}

// parsePrompts reads top-level `prompt` nodes, siblings of `wrap` in the same
// position as `icon`:
//
//	prompt "triage" title="Triage an incident" {
//	    description "Walk the on-call first-response steps"
//	    argument "service" description="Which service is failing" required=#true
//	    text "You are triaging {service}."
//	    text "Read ward://runbook/oncall before acting."
//	}
//
// The single argument is the prompt name. `text` children form the message
// body, joined with newlines. `{name}` is substituted with the argument the
// client supplies; an unsupplied optional argument substitutes to empty.
// The body is static text, so a prompt reaches no upstream and needs no guard.
func parsePrompts(src []byte) ([]inlinePrompt, error) {
	doc, err := parseInlineDoc(src, "prompts")
	if err != nil {
		return nil, err
	}
	var out []inlinePrompt
	seen := map[string]bool{}
	for _, n := range doc.Nodes {
		if n.Name() != "prompt" {
			continue
		}
		name, err := oneStringArg(n, "prompt")
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("ward-mcp: duplicate `prompt` name %q", name)
		}
		seen[name] = true
		p := mcp.Prompt{Name: name}
		for key, value := range n.Properties() {
			switch key {
			case "title":
				p.Title = value.String()
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `prompt` property %q (want title; fail-closed)", key)
			}
		}
		for _, child := range n.Children().Nodes {
			switch child.Name() {
			case "description":
				if len(child.Arguments()) != 1 {
					return nil, fmt.Errorf("ward-mcp: `prompt` %q child `description` wants exactly one argument", name)
				}
				p.Description = child.Arg(0).String()
			case "argument":
				arg, err := promptArgument(child, name)
				if err != nil {
					return nil, err
				}
				p.Arguments = append(p.Arguments, arg)
			case "text":
				// Collected by joinTextChildren below.
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `prompt` child %q (want description | argument | text; fail-closed)", child.Name())
			}
		}
		body, err := joinTextChildren(n, "prompt")
		if err != nil {
			return nil, err
		}
		if body == "" {
			return nil, fmt.Errorf("ward-mcp: `prompt` %q has no `text` content", name)
		}
		out = append(out, inlinePrompt{prompt: p, body: body})
	}
	return out, nil
}

func promptArgument(n *kdl.Node, promptName string) (*mcp.PromptArgument, error) {
	argName, err := oneStringArg(n, "argument")
	if err != nil {
		return nil, err
	}
	arg := &mcp.PromptArgument{Name: argName}
	for key, value := range n.Properties() {
		switch key {
		case "description":
			arg.Description = value.String()
		case "required":
			required, err := boolProp(value, "argument", key)
			if err != nil {
				return nil, err
			}
			arg.Required = required
		case "title":
			arg.Title = value.String()
		default:
			return nil, fmt.Errorf("ward-mcp: unknown `argument` property %q on prompt %q (want description | required | title; fail-closed)", key, promptName)
		}
	}
	return arg, nil
}

// registerPrompts adds each parsed prompt to the SDK server. Registering any
// prompt is what makes the SDK infer the `prompts` capability, so a spec with
// no `prompt` node advertises none.
func (s *Server) registerPrompts(prompts []inlinePrompt) {
	for _, p := range prompts {
		prompt := p
		s.prompts = append(s.prompts, prompt.prompt)
		s.sdk.AddPrompt(&prompt.prompt, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			body, err := renderPrompt(prompt, req.Params.Arguments)
			if err != nil {
				return nil, err
			}
			return &mcp.GetPromptResult{
				Description: prompt.prompt.Description,
				Messages: []*mcp.PromptMessage{{
					Role:    "user",
					Content: &mcp.TextContent{Text: body},
				}},
			}, nil
		})
	}
}

// renderPrompt substitutes `{name}` placeholders. A missing required argument
// is an error rather than an empty substitution: a half-filled prompt reads as
// a complete one to the model, which is worse than a refusal.
func renderPrompt(p inlinePrompt, args map[string]string) (string, error) {
	body := p.body
	for _, arg := range p.prompt.Arguments {
		value, ok := args[arg.Name]
		if !ok && arg.Required {
			return "", fmt.Errorf("ward-mcp: prompt %q needs argument %q", p.prompt.Name, arg.Name)
		}
		body = strings.ReplaceAll(body, "{"+arg.Name+"}", value)
	}
	return body, nil
}
