package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// callThingTool fires get_thing against a stub upstream and returns the raw
// tool-result content text - the exact bytes a consuming harness would slice.
func callThingTool(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "s3cr3t")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-flight-deck","id":"42"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("tool result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("tool call reported isError; content=%v", result["content"])
	}
	return firstText(t, result)
}

// The invariant this whole issue turns on. A consuming harness bounds a tool
// result by keeping the front and discarding the tail, so a caveat serialized
// last is destroyed first and deterministically - the model then reads rows
// with no caveat and answers as though the view were complete.
func TestCoverageSerializesBeforeThePayload(t *testing.T) {
	text := callThingTool(t, `{"id":42,"rows":[1,2,3]}`)

	coverageAt := strings.Index(text, `"coverage"`)
	resultAt := strings.Index(text, `"result"`)
	if coverageAt < 0 || resultAt < 0 {
		t.Fatalf("envelope missing coverage or result: %s", text)
	}
	if coverageAt > resultAt {
		t.Fatalf("coverage serialized after the payload at %d vs %d: %s", coverageAt, resultAt, text)
	}
	// Not merely "earlier than the payload" - a head slice has to keep it, and
	// the smallest cap observed in the fleet was 8192.
	if coverageAt > 64 {
		t.Errorf("coverage starts at byte %d, too deep to survive a head slice", coverageAt)
	}
}

// A count in meaning is what changes an answer. "3152 of 22933" is actionable;
// a byte total alone is not, which is how a 528-of-22933 view was reported as
// "none exists".
func TestCoverageCountsEveryArrayInThePayload(t *testing.T) {
	var payload toolPayload
	if err := json.Unmarshal([]byte(callThingTool(t, `{"rows":[1,2,3],"labels":["a","b"],"id":42}`)), &payload); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	for name, want := range map[string]int{"rows": 3, "labels": 2} {
		if got := payload.Coverage.Items[name]; got != want {
			t.Errorf("coverage.items[%q] = %d, want %d", name, got, want)
		}
	}
	if _, counted := payload.Coverage.Items["id"]; counted {
		t.Errorf("coverage counted a non-array field: %v", payload.Coverage.Items)
	}
}

func TestCoverageCountsATopLevelArray(t *testing.T) {
	var payload toolPayload
	if err := json.Unmarshal([]byte(callThingTool(t, `[1,2,3,4]`)), &payload); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if got := payload.Coverage.Items["result"]; got != 4 {
		t.Errorf("coverage.items[result] = %d, want 4", got)
	}
}

// Truncation is stated rather than omitted. A standing explicit claim is what
// lets a consumer attribute a short view to its own slicing rather than here.
func TestCoverageStatesNoTruncationAndFlagsAnOverBudgetResponse(t *testing.T) {
	small := toolPayload{}
	if err := json.Unmarshal([]byte(callThingTool(t, `{"id":42}`)), &small); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if small.Coverage.Truncated {
		t.Errorf("small response reported truncated: %+v", small.Coverage)
	}
	if small.Coverage.OverBudget {
		t.Errorf("small response reported over_budget: %+v", small.Coverage)
	}
	if small.Coverage.Bytes == 0 {
		t.Errorf("small response reported zero bytes: %+v", small.Coverage)
	}

	// An array the upstream never bounded is the shape that overruns a
	// consumer cap. It still arrives whole - nothing here truncates - but it
	// arrives saying so.
	rows := make([]string, 0, 400)
	for range 400 {
		rows = append(rows, `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`)
	}
	big := toolPayload{}
	if err := json.Unmarshal([]byte(callThingTool(t, `{"rows":[`+strings.Join(rows, ",")+`]}`)), &big); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if !big.Coverage.OverBudget {
		t.Errorf("a %d-byte response did not report over_budget: %+v", big.Coverage.Bytes, big.Coverage)
	}
	if big.Coverage.Truncated {
		t.Errorf("over-budget response claimed truncation it did not perform: %+v", big.Coverage)
	}
	if got := big.Coverage.Items["rows"]; got != 400 {
		t.Errorf("coverage.items[rows] = %d, want 400", got)
	}
	if big.Coverage.Bytes <= consumerBudgetBytes {
		t.Errorf("coverage.bytes = %d, want over the %d budget", big.Coverage.Bytes, consumerBudgetBytes)
	}
}

// An unreadable dataset must not serialize like a measured zero. This runtime
// never synthesizes one: an upstream it cannot read is a tool error, and a
// null payload stays null.
func TestUnreadableUpstreamIsAnErrorRatherThanAnEmptyResult(t *testing.T) {
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "s3cr3t")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream is down", http.StatusBadGateway)
	}))
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-flight-deck","id":"42"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("tool result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("an unreadable upstream returned a success result: %v", result)
	}
	if got := firstText(t, result); strings.Contains(got, `"items":{}`) || strings.Contains(got, `"result":[]`) {
		t.Errorf("an unreadable upstream serialized like a measured zero: %s", got)
	}
}

// The text and structured halves must agree. They used to disagree - text
// carried the bare upstream body while structured carried {"result": ...} -
// and the text half is the one a consumer slices.
func TestTextAndStructuredContentCarryTheSameEnvelope(t *testing.T) {
	t.Setenv("MCP_BEAVER_TEST_TOKEN", "s3cr3t")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"rows":[1,2]}`))
	}))
	defer upstream.Close()

	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_thing","arguments":{"owner":"coilyco-flight-deck","id":"42"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("tool result: %v", err)
	}

	var fromText, fromStructured any
	if err := json.Unmarshal([]byte(firstText(t, result)), &fromText); err != nil {
		t.Fatalf("text content is not the envelope: %v", err)
	}
	structured, err := json.Marshal(result["structuredContent"])
	if err != nil {
		t.Fatalf("structuredContent: %v", err)
	}
	if err := json.Unmarshal(structured, &fromStructured); err != nil {
		t.Fatalf("structuredContent is not JSON: %v", err)
	}
	textJSON, _ := json.Marshal(fromText)
	structuredJSON, _ := json.Marshal(fromStructured)
	if string(textJSON) != string(structuredJSON) {
		t.Fatalf("text and structured disagree:\n text = %s\n struct = %s", textJSON, structuredJSON)
	}
}

// The shape every GraphQL response has: the array sits under `data`, two
// objects down. Walking one level reported no count at all, which reads as
// "nothing was returned" (mcp-beaver#88).
func TestCoverageCountsAnArrayNestedUnderObjects(t *testing.T) {
	var payload toolPayload
	body := `{"data":{"Page":{"media":[{"id":1},{"id":5},{"id":17205}]}}}`
	if err := json.Unmarshal([]byte(callThingTool(t, body)), &payload); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if got := payload.Coverage.Items["data.Page.media"]; got != 3 {
		t.Fatalf("coverage.items = %v, want data.Page.media = 3", payload.Coverage.Items)
	}
}

// The per-row rationale the one-level rule was written for still holds: an
// array inside an array ELEMENT is a detail of one row, not of the view.
func TestCoverageDoesNotCountArraysInsideArrayElements(t *testing.T) {
	var payload toolPayload
	body := `{"rows":[{"tags":["a","b"]},{"tags":["c"]}]}`
	if err := json.Unmarshal([]byte(callThingTool(t, body)), &payload); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if got := payload.Coverage.Items["rows"]; got != 2 {
		t.Errorf("coverage.items[rows] = %d, want 2", got)
	}
	for name := range payload.Coverage.Items {
		if name != "rows" {
			t.Errorf("coverage counted a per-row array: %v", payload.Coverage.Items)
		}
	}
}

// Nothing already emitted changes spelling: a top-level array stays "result"
// and a top-level named array keeps its bare key.
func TestCoverageKeepsTopLevelKeysBare(t *testing.T) {
	var payload toolPayload
	if err := json.Unmarshal([]byte(callThingTool(t, `{"rows":[1,2],"page":{"labels":["a"]}}`)), &payload); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if got := payload.Coverage.Items["rows"]; got != 2 {
		t.Errorf("coverage.items[rows] = %d, want 2 with no prefix", got)
	}
	if got := payload.Coverage.Items["page.labels"]; got != 1 {
		t.Errorf("coverage.items[page.labels] = %d, want 1", got)
	}
}

// The descent is bounded, so a deeply nested payload cannot make the coverage
// block the expensive part of the response.
func TestCoverageBoundsTheObjectDescent(t *testing.T) {
	deep := map[string]any{"leaf": []any{1, 2}}
	for range maxCountDepth + 2 {
		deep = map[string]any{"nest": deep}
	}
	if counts := countArrays(deep); len(counts) != 0 {
		t.Fatalf("counted past the depth bound: %v", counts)
	}
	shallow := countArrays(map[string]any{"a": map[string]any{"b": []any{1}}})
	if shallow["a.b"] != 1 {
		t.Fatalf("a reachable nested array was missed: %v", shallow)
	}
}
