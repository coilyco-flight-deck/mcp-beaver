package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ArgPin fixes one argument of one proxied tool to one exact value, applied by
// the wrapper rather than supplied by the caller.
//
// `upstream.tools` allowlists tool NAMES, which is the whole authority only
// while the verb carries the scope. It fails whenever scope rides in an
// argument instead: allowlisting one Bluesky read tool grants every account,
// because the account is a parameter (deploy#358).
//
// # What this deliberately does not do
//
// #56 also asks for conjunctive pinning of free-form filter EXPRESSIONS, so a
// caller-supplied filter narrows and never widens - the SigNoz case
// (deploy#359). That is not implemented here, and the omission is the point
// rather than an oversight.
//
// Conjoining expressions requires knowing the upstream's query language. A
// pin that is AND-ed wrongly does not fail loudly, it silently widens, and the
// consumer is an agent with a public output surface where a read grant is an
// unattributable exfiltration path. The issue itself declined to invent that
// mechanism against a live surface on spec, and that judgement holds here.
//
// Exact-value pinning has no such ambiguity: the value either matches or the
// call is refused. It covers the identity-scoped cases and nothing more.
type ArgPin struct {
	Tool  string
	Arg   string
	Value string
}

// ParseArgPin reads the `<tool>.<arg>=<value>` CLI form.
func ParseArgPin(raw string) (ArgPin, error) {
	spec, value, found := strings.Cut(raw, "=")
	if !found {
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q must be <tool>.<arg>=<value>", raw)
	}
	tool, arg, found := strings.Cut(strings.TrimSpace(spec), ".")
	if !found {
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q must name the tool and the argument, as <tool>.<arg>=<value>", raw)
	}
	pin := ArgPin{Tool: strings.TrimSpace(tool), Arg: strings.TrimSpace(arg), Value: value}
	switch {
	case pin.Tool == "":
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q names an empty tool", raw)
	case pin.Arg == "":
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q names an empty argument", raw)
	case pin.Value == "":
		// An empty pin would read as "unscoped" while looking configured.
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q has an empty value; a pin that scopes to nothing is not a scope", raw)
	}
	return pin, nil
}

// ValidatePins rejects pins that name a tool outside the allowlist, and pins
// that contradict each other. A pin on an unserved tool is the dangerous
// case: the operator believes a surface is scoped and nothing is applying it.
func ValidatePins(pins []ArgPin, allowlist []string) error {
	served := make(map[string]bool, len(allowlist))
	for _, tool := range allowlist {
		served[tool] = true
	}
	seen := map[string]string{}
	for _, pin := range pins {
		if !served[pin.Tool] {
			return fmt.Errorf("mcp-beaver: pin names tool %q, which is not in the allowlist: nothing would apply it", pin.Tool)
		}
		key := pin.Tool + "." + pin.Arg
		if prior, dup := seen[key]; dup && prior != pin.Value {
			return fmt.Errorf("mcp-beaver: conflicting pins for %s (%q and %q)", key, prior, pin.Value)
		}
		seen[key] = pin.Value
	}
	return nil
}

// pinsByTool groups validated pins for dispatch.
func pinsByTool(pins []ArgPin) map[string][]ArgPin {
	if len(pins) == 0 {
		return nil
	}
	out := map[string][]ArgPin{}
	for _, pin := range pins {
		out[pin.Tool] = append(out[pin.Tool], pin)
	}
	for _, group := range out {
		sort.Slice(group, func(i, j int) bool { return group[i].Arg < group[j].Arg })
	}
	return out
}

// withArgPins applies the tool's pins to every call.
//
// A caller that supplies the pinned argument with a different value is
// REFUSED rather than silently corrected. Silently overwriting would let a
// model believe it had read one scope while reading another, and a refusal is
// the only outcome a prompt injection cannot turn into a wider read.
func withArgPins(pins []ArgPin, next mcp.ToolHandler) mcp.ToolHandler {
	if len(pins) == 0 {
		return next
	}
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := map[string]any{}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
			}
		}
		for _, pin := range pins {
			if supplied, ok := args[pin.Arg]; ok {
				if fmt.Sprintf("%v", supplied) != pin.Value {
					return toolError(fmt.Errorf(
						"mcp-beaver: %s is pinned to %q on this server and cannot be set to %v",
						pin.Arg, pin.Value, supplied,
					)), nil
				}
				continue
			}
			args[pin.Arg] = pin.Value
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return toolError(err), nil
		}
		cloned := *req
		params := *req.Params
		params.Arguments = raw
		cloned.Params = &params
		return next(ctx, &cloned)
	}
}
