package mcpserver

import (
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// parseIcons reads top-level `icon` nodes from a `.mcp.kdl` document and
// projects them into the Implementation icons the initialize response carries
// (`serverInfo.icons`), so clients that render server icons - the claude.ai /
// ChatGPT connector tile - show a brand mark instead of a placeholder
// (deploy#255).
//
// Grammar - ONE construct past the frozen inline grammar (ward-mcp#6), ruled
// in on deploy#255. The node rides BESIDE `wrap` at the document top level,
// which `opcore.ParseInline` never inspects (it reads only the `wrap` node),
// so the frozen wrap-body grammar and the umbra pin are untouched:
//
//	icon "data:image/png;base64,..."
//	icon "https://static.example/icon.svg" mime="image/svg+xml" sizes="any"
//
// The single argument is the icon src, required. `mime` is optional - a
// `data:` src infers it from its own prefix. `sizes` is an optional single
// token ("256x256", "any"). Repeat the node for size or theme variants.
// Prefer `data:` URIs: the gated deploys sit behind oauth2-proxy, where a
// hosted icon URL would 401 for the connecting client, and a data URI rides
// inside the initialize payload with no external dependency.
func parseIcons(src []byte) ([]mcp.Icon, error) {
	doc, err := parseInlineDoc(src, "icons")
	if err != nil {
		return nil, err
	}

	var icons []mcp.Icon
	for _, n := range doc.Nodes {
		if n.Name() != "icon" {
			continue
		}
		if len(n.Arguments()) != 1 {
			return nil, fmt.Errorf("ward-mcp: `icon` wants exactly one src argument, got %d", len(n.Arguments()))
		}
		iconSrc := n.Arg(0).String()
		if iconSrc == "" {
			return nil, fmt.Errorf("ward-mcp: `icon` src must be non-empty")
		}
		icon := mcp.Icon{Source: iconSrc}
		for key, value := range n.Properties() {
			switch key {
			case "mime":
				icon.MIMEType = value.String()
			case "sizes":
				icon.Sizes = []string{value.String()}
			default:
				return nil, fmt.Errorf("ward-mcp: unknown `icon` property %q (want mime | sizes; fail-closed)", key)
			}
		}
		if icon.MIMEType == "" {
			icon.MIMEType = dataURIMIME(iconSrc)
		}
		icons = append(icons, icon)
	}
	return icons, nil
}

// dataURIMIME extracts the MIME type a `data:` URI already states, so the
// guardfile does not have to repeat it. Non-data srcs return "" and the field
// stays unset.
func dataURIMIME(src string) string {
	rest, ok := strings.CutPrefix(src, "data:")
	if !ok {
		return ""
	}
	end := strings.IndexAny(rest, ";,")
	if end <= 0 {
		return ""
	}
	return rest[:end]
}
