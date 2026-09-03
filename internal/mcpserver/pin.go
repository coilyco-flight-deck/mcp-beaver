package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	kdl "github.com/calico32/kdl-go"
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
//
// The pinned value is a value chain of one, resolved at CALL time the way
// `auth` and a spec-mode query pin resolve: nothing is baked into the image,
// and a rotated env var takes effect on the next call rather than the next
// restart. The CLI form states `literal`, so a flag and a guardfile `pin`
// reach the same resolver.
type ArgPin struct {
	Tool     string
	Arg      string
	Provider string // env | file | literal, or a minted oauth2 client
	Source   string // env var name, file path, or the literal value
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
	pin := ArgPin{Tool: strings.TrimSpace(tool), Arg: strings.TrimSpace(arg), Provider: literalProvider, Source: value}
	switch {
	case pin.Tool == "":
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q names an empty tool", raw)
	case pin.Arg == "":
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q names an empty argument", raw)
	case pin.Source == "":
		// An empty pin would read as "unscoped" while looking configured.
		return ArgPin{}, fmt.Errorf("mcp-beaver: pin %q has an empty value; a pin that scopes to nothing is not a scope", raw)
	}
	return pin, nil
}

// literalProvider is the value source a `--pin` flag states. It is named so
// the flag form and the guardfile node below read as one mechanism rather than
// two that happen to agree.
const literalProvider = "literal"

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
		// Compared as (provider, source) rather than as a resolved value: two
		// pins on one argument contradict each other whether or not the two
		// sources happen to hold the same string today.
		chain := pin.Provider + " " + pin.Source
		if prior, dup := seen[key]; dup && prior != chain {
			return fmt.Errorf("mcp-beaver: conflicting pins for %s (%s and %s)", key, prior, chain)
		}
		seen[key] = chain
	}
	return nil
}

// ValidatePinSources rejects a pin whose value source this server cannot
// resolve, offline. A guardfile-stated pin can name any provider the registry
// carries, so a typo is catchable at `lint` instead of at the first call.
func ValidatePinSources(pins []ArgPin, providers ProviderSet) error {
	for _, pin := range pins {
		if err := providers.checkSource(pin.Provider, pin.Source); err != nil {
			return fmt.Errorf("mcp-beaver: `pin` %q: argument %q %w", pin.Tool, pin.Arg, err)
		}
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
//
// The refusal names the argument and the caller's own value, never the pinned
// one: a pin resolved from `env` or a minted client holds a secret, and a
// refusal is exactly when a payload is most likely to be logged.
func withArgPins(pins []ArgPin, providers ProviderSet, next mcp.ToolHandler) mcp.ToolHandler {
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
			value, err := resolveArgPin(ctx, pin, providers)
			if err != nil {
				return toolError(err), nil
			}
			if supplied, ok := args[pin.Arg]; ok {
				if fmt.Sprintf("%v", supplied) != value {
					return toolError(fmt.Errorf(
						"mcp-beaver: %s is pinned on this server and cannot be set to %v",
						pin.Arg, supplied,
					)), nil
				}
				continue
			}
			args[pin.Arg] = value
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

// parseArgPins reads top-level `pin` nodes on an `mcp-upstream` guardfile:
//
//	pin "get_author_feed" {
//	    argument "actor" env "BSKY_ACTOR"
//	    argument "limit" literal "25"
//	}
//
// The same node a REST guardfile states, and the child says which surface the
// pin lands on: `query` fixes an outgoing query parameter opcore builds into a
// URL, `argument` fixes a named argument the proxy forwards. A proxy sees only
// `argument`, so a `query` child here is refused rather than parsed and
// dropped, and the same node in a REST guardfile refuses `argument`.
//
// The argument is the PROJECTED TOOL NAME, matching `confirm` and `withhold`.
func parseArgPins(sources []guardSource, providers ProviderSet) ([]ArgPin, error) {
	nodes, err := parseInlineNodes(sources, "pin")
	if err != nil {
		return nil, err
	}
	var out []ArgPin
	seen := map[string]bool{}
	for _, sn := range nodes {
		n := sn.node
		if n.Name() != "pin" {
			continue
		}
		tool, err := oneStringArg(n, "pin")
		if err != nil {
			return nil, err
		}
		if seen[tool] {
			return nil, fmt.Errorf("mcp-beaver: duplicate `pin` for tool %q", tool)
		}
		seen[tool] = true
		if len(n.Properties()) > 0 {
			return nil, fmt.Errorf("mcp-beaver: `pin` %q takes no properties, only `argument` children (fail-closed)", tool)
		}
		pins, err := parseArgPinChildren(n, tool)
		if err != nil {
			return nil, err
		}
		out = append(out, pins...)
	}
	if err := ValidatePinSources(out, providers); err != nil {
		return nil, err
	}
	return out, nil
}

func parseArgPinChildren(n *kdl.Node, tool string) ([]ArgPin, error) {
	var pins []ArgPin
	seen := map[string]bool{}
	for _, child := range n.Children().Nodes {
		if child.Name() != "argument" {
			return nil, fmt.Errorf("mcp-beaver: unknown `pin` child %q on a proxy guardfile (want argument; `query` is the REST form and reaches no proxied tool; fail-closed)", child.Name())
		}
		args := child.Arguments()
		if len(args) != 3 {
			return nil, fmt.Errorf("mcp-beaver: `pin` %q: `argument` wants three arguments - name, provider, source - e.g. `argument \"actor\" env \"BSKY_ACTOR\"` (got %d)", tool, len(args))
		}
		if len(child.Properties()) > 0 {
			return nil, fmt.Errorf("mcp-beaver: `pin` %q: `argument` takes no properties (fail-closed)", tool)
		}
		pin := ArgPin{Tool: tool, Arg: args[0].String(), Provider: args[1].String(), Source: args[2].String()}
		switch {
		case pin.Arg == "":
			return nil, fmt.Errorf("mcp-beaver: `pin` %q: argument name must be non-empty", tool)
		case pin.Source == "":
			return nil, fmt.Errorf("mcp-beaver: `pin` %q: argument %q has an empty source; a pin that scopes to nothing is not a scope", tool, pin.Arg)
		}
		if seen[pin.Arg] {
			return nil, fmt.Errorf("mcp-beaver: `pin` %q pins argument %q twice", tool, pin.Arg)
		}
		seen[pin.Arg] = true
		pins = append(pins, pin)
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("mcp-beaver: `pin` %q states no `argument` children: a pin that fixes nothing is not a pin", tool)
	}
	return pins, nil
}

// resolveArgPin reads one pin's source, at call time like every other value
// this server resolves. The error names the argument and never the value.
func resolveArgPin(ctx context.Context, pin ArgPin, providers ProviderSet) (string, error) {
	provider, ok := providers.registry()[pin.Provider]
	if !ok {
		return "", fmt.Errorf("mcp-beaver: pinned argument %q names unknown value provider %q", pin.Arg, pin.Provider)
	}
	value, err := provider(ctx, pin.Source)
	if err != nil {
		return "", fmt.Errorf("mcp-beaver: resolve pinned argument %q: %w", pin.Arg, err)
	}
	if value == "" {
		return "", fmt.Errorf("mcp-beaver: pinned argument %q resolved empty; a pin that scopes to nothing is not a scope", pin.Arg)
	}
	return value, nil
}
