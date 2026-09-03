package mcpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestParseArgPin(t *testing.T) {
	pin, err := ParseArgPin("get_author_feed.actor=kai.bsky.social")
	if err != nil {
		t.Fatalf("ParseArgPin: %v", err)
	}
	if pin.Tool != "get_author_feed" || pin.Arg != "actor" || pin.Provider != literalProvider || pin.Source != "kai.bsky.social" {
		t.Errorf("pin = %+v, want the tool, argument, and value split out", pin)
	}
	for _, raw := range []string{"", "notool", "tool.arg", "tool.=v", ".arg=v", "tool.arg=", "=v"} {
		if _, err := ParseArgPin(raw); err == nil {
			t.Errorf("ParseArgPin(%q) was accepted, want fail-closed", raw)
		}
	}
}

// A pin naming a tool nobody serves is the dangerous shape: the operator
// believes a surface is scoped and nothing is applying it.
func TestValidatePinsRejectsUnservedTool(t *testing.T) {
	err := ValidatePins([]ArgPin{{Tool: "absent", Arg: "actor", Provider: literalProvider, Source: "x"}}, []string{"browse"})
	if err == nil {
		t.Fatal("a pin on an unallowlisted tool was accepted")
	}
	if !strings.Contains(err.Error(), "nothing would apply it") {
		t.Errorf("error = %q, want it to name the consequence", err)
	}
	if err := ValidatePins([]ArgPin{{Tool: "browse", Arg: "q", Provider: literalProvider, Source: "x"}}, []string{"browse"}); err != nil {
		t.Errorf("a pin on an allowlisted tool was rejected: %v", err)
	}
	conflict := []ArgPin{
		{Tool: "browse", Arg: "q", Provider: literalProvider, Source: "a"},
		{Tool: "browse", Arg: "q", Provider: literalProvider, Source: "b"},
	}
	if err := ValidatePins(conflict, []string{"browse"}); err == nil {
		t.Error("contradicting pins were accepted")
	}
}

func pinnedProxy(t *testing.T, pins []ArgPin) (*httptest.Server, func()) {
	t.Helper()
	upstreamServer := upstreamTool(t, "browse", "browse upstream", `{
		"type":"object",
		"properties":{"q":{"type":"string"},"scope":{"type":"string"}}
	}`, func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
		scope, _ := args["scope"].(string)
		q, _ := args["q"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "scope=" + scope + " q=" + q}}}, nil, nil
	})
	upstream := newUpstreamServer(t, upstreamServer)
	s, err := NewProxyWithPins(context.Background(), "proxy", "", upstream.URL+"/mcp",
		[]string{"browse"}, pins, upstream.Client())
	if err != nil {
		upstream.Close()
		t.Fatalf("NewProxyWithPins: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	return ts, func() {
		ts.Close()
		_ = s.Close()
		upstream.Close()
	}
}

// The pin is supplied by the wrapper, not the caller: a call that names no
// scope still reaches the upstream scoped.
func TestArgPinAppliesWhenCallerOmitsIt(t *testing.T) {
	pins := []ArgPin{{Tool: "browse", Arg: "scope", Provider: literalProvider, Source: "sirens-deep"}}
	ts, done := pinnedProxy(t, pins)
	defer done()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/browse", "application/json", `{"q":"ramen"}`)
	body := decodeAPIResult(t, resp)
	content, _ := body["content"].([]any)
	first, _ := content[0].(map[string]any)
	if first["text"] != "scope=sirens-deep q=ramen" {
		t.Errorf("upstream saw %v, want the pinned scope applied", first["text"])
	}
}

// Non-overridable, and refused rather than silently corrected. Quietly
// rewriting would let a model believe it read one scope while reading another,
// and a refusal is the outcome a prompt injection cannot widen.
func TestArgPinRefusesAnOverride(t *testing.T) {
	pins := []ArgPin{{Tool: "browse", Arg: "scope", Provider: literalProvider, Source: "sirens-deep"}}
	ts, done := pinnedProxy(t, pins)
	defer done()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/browse", "application/json",
		`{"q":"ramen","scope":"kube-system"}`)
	body := decodeAPIResult(t, resp)
	if body["isError"] != true {
		t.Fatalf("body = %v, want the call refused", body)
	}
	content, _ := body["content"].([]any)
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if !strings.Contains(text, "pinned") {
		t.Errorf("error = %q, want it to say the argument is pinned", text)
	}
	if !strings.Contains(text, "kube-system") {
		t.Errorf("error = %q, want it to name the rejected value", text)
	}
}

// Supplying the pinned value itself is not an attack and must pass.
func TestArgPinAllowsTheMatchingValue(t *testing.T) {
	pins := []ArgPin{{Tool: "browse", Arg: "scope", Provider: literalProvider, Source: "sirens-deep"}}
	ts, done := pinnedProxy(t, pins)
	defer done()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/browse", "application/json",
		`{"q":"ramen","scope":"sirens-deep"}`)
	body := decodeAPIResult(t, resp)
	if body["isError"] == true {
		t.Fatalf("body = %v, want the matching value accepted", body)
	}
}

// An unpinned tool is untouched, so pins never become a blanket change to the
// proxied surface.
func TestUnpinnedProxyIsUnchanged(t *testing.T) {
	ts, done := pinnedProxy(t, nil)
	defer done()

	resp := postAPI(t, ts.Client(), ts.URL+"/api/browse", "application/json", `{"q":"ramen"}`)
	body := decodeAPIResult(t, resp)
	content, _ := body["content"].([]any)
	first, _ := content[0].(map[string]any)
	if first["text"] != "scope= q=ramen" {
		t.Errorf("upstream saw %v, want the call passed through untouched", first["text"])
	}
}
