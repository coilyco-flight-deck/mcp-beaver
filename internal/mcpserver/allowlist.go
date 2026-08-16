package mcpserver

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// mutationVerbs are the name segments that mark a tool as changing upstream
// state. Kept deliberately narrow: a false positive blocks a legitimate
// read-only tool, and `lint-upstream --upstream` supersedes this heuristic
// with the upstream's own readOnlyHint. See docs/FEATURES.md.
var mutationVerbs = map[string]struct{}{
	"add": {}, "apply": {}, "cancel": {}, "create": {}, "delete": {},
	"disable": {}, "drop": {}, "edit": {}, "enable": {}, "grant": {},
	"import": {}, "insert": {}, "patch": {}, "publish": {}, "purge": {},
	"put": {}, "remove": {}, "rename": {}, "reset": {}, "revoke": {},
	"send": {}, "set": {}, "update": {}, "upsert": {}, "write": {},
}

// ValidateAllowlist checks an upstream tool allowlist for the shape the proxy
// requires and returns the trimmed names in their original order. It is the
// one authority on allowlist well-formedness, shared by the serving path and
// by `mcp-beaver lint-upstream`, so a consumer never writes a second validator.
func ValidateAllowlist(tools []string) ([]string, error) {
	if len(tools) == 0 {
		return nil, fmt.Errorf("mcp-beaver: upstream allowlist is empty")
	}
	out := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for i, tool := range tools {
		name := strings.TrimSpace(tool)
		if name == "" {
			return nil, fmt.Errorf("mcp-beaver: upstream allowlist entry %d is empty", i+1)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("mcp-beaver: duplicate upstream tool allowlist entry %q", name)
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// MutationSuspects returns the allowlist entries whose names carry a mutation
// verb, sorted. It is a naming heuristic, not upstream truth: it exists so an
// offline reviewer catches a mutation tool entering a read-only allowlist
// without a live upstream connection.
func MutationSuspects(tools []string) []string {
	var out []string
	for _, tool := range tools {
		for _, segment := range nameSegments(tool) {
			if _, ok := mutationVerbs[segment]; ok {
				out = append(out, tool)
				break
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// nameSegments lowercases and splits a tool name on the boundaries real MCP
// servers use: underscores, hyphens, dots, and camelCase humps. Segmenting
// rather than substring-matching is what keeps `get_field_values` clear of the
// "set" verb.
func nameSegments(name string) []string {
	fields := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' || r == ' '
	})
	var out []string
	for _, field := range fields {
		for _, hump := range splitCamel(field) {
			out = append(out, strings.ToLower(hump))
		}
	}
	return out
}

func splitCamel(field string) []string {
	var out []string
	start := 0
	runes := []rune(field)
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) && !unicode.IsUpper(runes[i-1]) {
			out = append(out, string(runes[start:i]))
			start = i
		}
	}
	return append(out, string(runes[start:]))
}
