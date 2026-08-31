package mcpserver

import (
	"fmt"
	"strings"
)

// serverInstructions is the policy floor every generated server publishes. It
// is a statement about how to treat the surface rather than a description of
// it, so it is true of every guardfile and stays shared.
const serverInstructions = "This server exposes only policy-approved tools. Use read-only tools to inspect state before mutation tools. Follow each tool's input and output schema, and treat safety annotations as hints rather than authorization."

// maxInstructionsLen bounds the guardfile's own text. A consumer that renders
// `InitializeResult.Instructions` into the model's prompt pays for it on every
// turn, once per rostered server, so this is a budget rather than a parser
// limit. Three or four sentences is what says which dam you are at; a page is
// a docs page.
const maxInstructionsLen = 500

// parseInstructions reads the optional top-level `instructions` node, a
// sibling of `wrap`:
//
//	instructions {
//	    text "Issues, pull requests and repository metadata on the Coilyco Forgejo."
//	    text "Reach for it to read or file tracker work."
//	}
//
// Every server used to publish the shared policy sentence and nothing else, so
// a client holding four of them learned that each exposes policy-approved
// tools - true of every one and distinguishing none (mcp-beaver#77). The
// protocol describes this field as guidance a client can use to improve the
// model's understanding of what the server is for, and a constant cannot do
// that.
//
// The shared sentence stays. This is what a guardfile adds under it, and a
// guardfile that declares nothing publishes exactly what it published before.
func parseInstructions(sources []guardSource) (string, error) {
	nodes, err := parseInlineNodes(sources, "instructions")
	if err != nil {
		return "", err
	}
	found := false
	text := ""
	for _, sn := range nodes {
		n := sn.node
		if n.Name() != "instructions" {
			continue
		}
		if found {
			return "", fmt.Errorf("mcp-beaver: duplicate `instructions` node (fail-closed)")
		}
		found = true
		if len(n.Arguments()) > 0 {
			return "", fmt.Errorf("mcp-beaver: `instructions` takes no arguments, only `text` children")
		}
		if len(n.Properties()) > 0 {
			return "", fmt.Errorf("mcp-beaver: `instructions` takes no properties, only `text` children")
		}
		for _, child := range n.Children().Nodes {
			if child.Name() != "text" {
				return "", fmt.Errorf("mcp-beaver: unknown `instructions` child %q (want text; fail-closed)", child.Name())
			}
		}
		text, err = joinTextChildren(n, "instructions")
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return "", fmt.Errorf("mcp-beaver: `instructions` needs at least one non-empty `text` child")
		}
		if len(text) > maxInstructionsLen {
			return "", fmt.Errorf("mcp-beaver: `instructions` is %d characters, over the %d-character budget a consumer carries every turn", len(text), maxInstructionsLen)
		}
	}
	return text, nil
}

// renderInstructions puts the guardfile's own text under the shared policy
// sentence. Ordering is deliberate: the policy floor is what a client must not
// lose, and a head-slicing consumer keeps the front (mcp-beaver#68).
func renderInstructions(own string) string {
	own = strings.TrimSpace(own)
	if own == "" {
		return serverInstructions
	}
	return serverInstructions + "\n\n" + own
}
