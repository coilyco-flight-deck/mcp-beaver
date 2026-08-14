package mcpserver

import (
	"context"
	"fmt"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/http/opcore"
	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/pkg/valuesource"
	kdl "github.com/calico32/kdl-go"
)

// queryPin fixes one outgoing query parameter for one spec-mode tool, resolved
// server-side and never exposed to the caller.
//
// This is the spec-mode counterpart to ArgPin, which does the same job for the
// upstream proxy. The need is identical and the mechanism differs because the
// two modes assemble a request differently: a proxy forwards named arguments,
// while spec mode derives a URL from the grant.
//
// The gap it closes is that `set` writes fixed BODY values only, so a GET
// endpoint whose scope rides in the query string had nowhere to put it. The
// only alternative was declaring the scope as a caller query field, which
// hands the model the very parameter the deployment is trying to fix - for
// Steam, that is the difference between "Kai's library" and "anyone's library".
type queryPin struct {
	Name     string // outgoing query parameter
	Provider string // env | file | literal
	Source   string // env var name, file path, or the literal value
}

// parseQueryPins reads top-level `pin` nodes, siblings of `wrap`:
//
//	pin "get_owned_games" {
//	    query "steamid" env "STEAM_STEAMID64"
//	    query "include_appinfo" literal "1"
//	}
//
// The argument is the PROJECTED TOOL NAME, matching `confirm` and `withhold`.
func parseQueryPins(src []byte) (map[string][]queryPin, error) {
	doc, err := parseInlineDoc(src, "pin")
	if err != nil {
		return nil, err
	}
	out := map[string][]queryPin{}
	for _, n := range doc.Nodes {
		if n.Name() != "pin" {
			continue
		}
		tool, err := oneStringArg(n, "pin")
		if err != nil {
			return nil, err
		}
		if _, dup := out[tool]; dup {
			return nil, fmt.Errorf("ward-mcp: duplicate `pin` for tool %q", tool)
		}
		if len(n.Properties()) > 0 {
			return nil, fmt.Errorf("ward-mcp: `pin` %q takes no properties, only `query` children (fail-closed)", tool)
		}
		pins, err := parseQueryPinChildren(n, tool)
		if err != nil {
			return nil, err
		}
		out[tool] = pins
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func parseQueryPinChildren(n *kdl.Node, tool string) ([]queryPin, error) {
	var pins []queryPin
	seen := map[string]bool{}
	for _, child := range n.Children().Nodes {
		if child.Name() != "query" {
			return nil, fmt.Errorf("ward-mcp: unknown `pin` child %q (want query; fail-closed)", child.Name())
		}
		args := child.Arguments()
		if len(args) != 3 {
			return nil, fmt.Errorf("ward-mcp: `pin` %q: `query` wants three arguments - name, provider, source - e.g. `query \"steamid\" env \"STEAM_STEAMID64\"`", tool)
		}
		pin := queryPin{Name: args[0].String(), Provider: args[1].String(), Source: args[2].String()}
		switch {
		case pin.Name == "":
			return nil, fmt.Errorf("ward-mcp: `pin` %q: query parameter name must be non-empty", tool)
		case pin.Source == "":
			return nil, fmt.Errorf("ward-mcp: `pin` %q: query %q has an empty source", tool, pin.Name)
		}
		if _, ok := valuesource.Builtins()[pin.Provider]; !ok {
			return nil, fmt.Errorf("ward-mcp: `pin` %q: query %q names unknown provider %q (want env | file | literal; fail-closed)", tool, pin.Name, pin.Provider)
		}
		if seen[pin.Name] {
			return nil, fmt.Errorf("ward-mcp: `pin` %q pins query %q twice", tool, pin.Name)
		}
		seen[pin.Name] = true
		pins = append(pins, pin)
	}
	if len(pins) == 0 {
		return nil, fmt.Errorf("ward-mcp: `pin` %q states no `query` children: a pin that fixes nothing is not a pin", tool)
	}
	return pins, nil
}

// validateQueryPins rejects a pin that names no minted tool, and one that
// collides with a declared query field.
//
// The collision case matters more than it looks. A pinned name that is also a
// caller-supplied field would reach opcore through both Args.Query and the
// caller's input, which errors at CALL time rather than build time - a
// deployment would look configured and fail on first use. Worse, an author
// could reasonably read the pin as overriding the field, when it does not.
func validateQueryPins(pins map[string][]queryPin, descs []opcore.Descriptor) error {
	byTool := make(map[string]opcore.Descriptor, len(descs))
	for _, d := range descs {
		if d.Proxy == nil {
			byTool[toolName(d)] = d
		}
	}
	for tool, group := range pins {
		desc, served := byTool[tool]
		if !served {
			return fmt.Errorf("ward-mcp: `pin` names tool %q, which this spec does not mint as a grant: nothing would apply it", tool)
		}
		declared := make(map[string]bool, len(desc.QueryFlags))
		for _, f := range desc.QueryFlags {
			declared[f.Name] = true
			declared[f.QueryName()] = true
		}
		for _, pin := range group {
			if declared[pin.Name] {
				return fmt.Errorf("ward-mcp: `pin` %q pins query %q, which the grant also declares as a caller input: remove one, since a pin does not override a supplied value", tool, pin.Name)
			}
		}
	}
	return nil
}

// resolveQueryPins reads each pin's source at CALL time, matching how `auth`
// resolves its value: nothing is baked into the image, and a rotated env var
// takes effect on the next call rather than the next restart.
func resolveQueryPins(ctx context.Context, pins []queryPin) (map[string]string, error) {
	providers := valuesource.Builtins()
	out := make(map[string]string, len(pins))
	for _, pin := range pins {
		provider, ok := providers[pin.Provider]
		if !ok {
			return nil, fmt.Errorf("ward-mcp: unknown value provider %q", pin.Provider)
		}
		value, err := provider(ctx, pin.Source)
		if err != nil {
			return nil, fmt.Errorf("ward-mcp: resolve pinned query %q: %w", pin.Name, err)
		}
		out[pin.Name] = value
	}
	return out, nil
}
