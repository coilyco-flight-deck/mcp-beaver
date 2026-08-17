package mcpserver

import (
	"encoding/json"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/http/opcore"
)

// consumerBudgetBytes is the smallest tool-result cap observed in a consuming
// harness (sirens-echo#449 measured 8192 on one profile and 16384 on another).
// It is not a limit this runtime enforces - nothing here truncates - it is the
// threshold past which a response STOPS being self-describing, because whether
// the model sees all of it becomes a property of which consumer read it.
const consumerBudgetBytes = 8192

// coverage states the bound of a result. It is the first field of every
// grant-backed response, and that position is the whole point: a consuming
// harness that bounds a tool result by head-slicing keeps the front and
// discards the tail, so a caveat serialized last is the first thing destroyed
// (mcp-beaver#68). A model then reads rows with no caveat and answers as
// though the view were complete.
type coverage struct {
	// Truncated is always false and is stated rather than omitted. Silent
	// truncation is prohibited, and a standing explicit claim is what lets a
	// consumer attribute a short view to its own slicing rather than to this
	// runtime.
	Truncated bool `json:"truncated"`
	// Bytes is the size of the upstream payload this result carries.
	Bytes int `json:"bytes"`
	// OverBudget reports Bytes past consumerBudgetBytes: this response is
	// large enough that some consumer will cut it, and the model may be
	// reading a prefix.
	OverBudget bool `json:"over_budget"`
	// Items counts each array in the payload, keyed by field name, or by
	// "result" when the payload is itself an array. A count in meaning is what
	// changes an answer; a byte total is not. This is also where an array the
	// upstream failed to bound becomes visible to the author on the first call
	// rather than after a day of investigation.
	Items map[string]int `json:"items,omitempty"`
}

// toolPayload is the response envelope. FIELD ORDER IS THE CONTRACT: Go
// serializes struct fields in declaration order, so coverage reaches the wire
// ahead of the bulk payload. A map would sort keys alphabetically and put this
// at the mercy of whatever the caveat field is named next.
type toolPayload struct {
	Coverage coverage `json:"coverage"`
	Result   any      `json:"result"`
}

var resultOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"coverage":{
			"type":"object",
			"description":"The bound of this result, stated before the payload so a truncating consumer cannot remove it.",
			"properties":{
				"truncated":{"type":"boolean"},
				"bytes":{"type":"integer"},
				"over_budget":{"type":"boolean"},
				"items":{"type":"object","additionalProperties":{"type":"integer"}}
			},
			"required":["truncated","bytes","over_budget"]
		},
		"result":{}
	},
	"required":["coverage","result"],
	"additionalProperties":false
}`)

// newCoverage measures what this runtime can honestly measure: the payload
// size and the length of every array in it. It never reports a shown-of-total,
// because the total lives upstream and this runtime never asked for it -
// inventing one would be exactly the confident wrong answer #68 is about.
func newCoverage(resp opcore.Response, result any) coverage {
	size := len(resp.Raw)
	if size == 0 {
		if encoded, err := json.Marshal(result); err == nil {
			size = len(encoded)
		}
	}
	return coverage{
		Truncated:  false,
		Bytes:      size,
		OverBudget: size > consumerBudgetBytes,
		Items:      countArrays(result),
	}
}

// countArrays names every array in the payload and how long it is. One level
// deep on purpose: an array nested inside an array element is a per-row detail
// rather than a statement about the view, and enumerating those would put the
// coverage block itself over budget.
func countArrays(result any) map[string]int {
	switch typed := result.(type) {
	case []any:
		return map[string]int{"result": len(typed)}
	case map[string]any:
		counts := map[string]int{}
		for key, value := range typed {
			if items, ok := value.([]any); ok {
				counts[key] = len(items)
			}
		}
		if len(counts) == 0 {
			return nil
		}
		return counts
	default:
		return nil
	}
}
