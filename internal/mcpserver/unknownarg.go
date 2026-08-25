package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// undeclaredArgs returns the supplied argument names the tool does not declare,
// sorted so the message is stable.
//
// It exists because dropping one is the worst available failure for a filter.
// `signoz_aggregate_logs` was called with a `searchText` its schema never
// declared, and the call returned the count of every log in the window with
// status success: 8,760,201 against 8,759,997 unfiltered, while the same
// negative control expressed as `filter` returned 0 (mcp-beaver#94). A query
// that cannot be honoured has to fail, because a large plausible number nobody
// can tell apart from an answer is not a smaller version of an error.
func undeclaredArgs(declared map[string]bool, supplied map[string]any) []string {
	var out []string
	for name := range supplied {
		if !declared[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// undeclaredArgError names what was refused and what the tool does declare, so
// the caller can tell a typo from a parameter that never existed.
func undeclaredArgError(tool string, unknown []string, declared map[string]bool) error {
	known := make([]string, 0, len(declared))
	for name := range declared {
		known = append(known, name)
	}
	sort.Strings(known)
	accepts := "it accepts none"
	if len(known) > 0 {
		accepts = "it accepts " + strings.Join(known, ", ")
	}
	return fmt.Errorf("%s does not accept %s; %s. The call was refused rather than run without it",
		tool, strings.Join(unknown, ", "), accepts)
}

// declaredProperties reads the property names off a tool's JSON Schema, and
// reports whether the schema is closed enough to refuse an unknown name.
//
// A schema that explicitly opens itself with `additionalProperties` other than
// false keeps the permissive contract it declares. An absent
// `additionalProperties` is treated as closed, which inverts the JSON Schema
// default on purpose: a guard is the layer that is stricter than the thing it
// guards, and the permissive default is what let #94 through.
func declaredProperties(inputSchema any) (declared map[string]bool, closed bool) {
	raw, err := json.Marshal(inputSchema)
	if err != nil {
		return nil, false
	}
	var doc struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties *json.RawMessage           `json:"additionalProperties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false
	}
	if doc.Properties == nil {
		return nil, false
	}
	if doc.AdditionalProperties != nil && strings.TrimSpace(string(*doc.AdditionalProperties)) != "false" {
		return nil, false
	}
	declared = make(map[string]bool, len(doc.Properties))
	for name := range doc.Properties {
		declared[name] = true
	}
	return declared, true
}
