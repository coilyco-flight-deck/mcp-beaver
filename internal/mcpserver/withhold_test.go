package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// withheldSpec states the exact pair of failures #54 recorded from one
// session: an absent edit-comment verb inferred to be a capability gap, and an
// absent `labels` parameter over-generalised into an absent capability whose
// tool was in the list the whole time.
func withheldSpec() string {
	return `withhold "edit_issue-comment" {
    reason "Comment edits are withheld here for audit-trail integrity."
    alternative "create_thing"
}

` + roundTripSpec("http://127.0.0.1:1")
}

func withheldServer(t *testing.T, spec string) *httptest.Server {
	t.Helper()
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return httptest.NewServer(s.Handler())
}

// Discoverable is the whole point: a stub the agent cannot see changes nothing
// about reasoning from a hole in the tool list.
func TestWithheldStubAppearsInToolList(t *testing.T) {
	ts := withheldServer(t, withheldSpec())
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var found map[string]any
	for _, tool := range toolList(t, decodeRPCResponse(t, resp)) {
		if tool["name"] == "edit_issue-comment" {
			found = tool
		}
	}
	if found == nil {
		t.Fatal("withheld stub not served in tools/list")
	}
	description, _ := found["description"].(string)
	if !strings.HasPrefix(description, "NOT AVAILABLE") {
		t.Errorf("description = %q, want it to lead with the refusal", description)
	}
	if !strings.Contains(description, "audit-trail integrity") {
		t.Errorf("description = %q, want the stated reason", description)
	}
	if !strings.Contains(description, "create_thing") {
		t.Errorf("description = %q, want the named alternative", description)
	}
}

// The schema-level marker is for the client, so it can tell a stub from a live
// tool without reading prose. #54 asks for exactly this separation.
func TestWithheldStubCarriesMachineReadableMarker(t *testing.T) {
	ts := withheldServer(t, withheldSpec())
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, tool := range toolList(t, decodeRPCResponse(t, resp)) {
		meta, _ := tool["_meta"].(map[string]any)
		switch tool["name"] {
		case "edit_issue-comment":
			if meta[withheldMetaKey] != true {
				t.Errorf("stub _meta = %v, want %s true", meta, withheldMetaKey)
			}
			if meta[withheldMetaKey+"/alternative"] != "create_thing" {
				t.Errorf("stub _meta = %v, want the alternative named", meta)
			}
		case "get_thing", "create_thing":
			if meta[withheldMetaKey] != nil {
				t.Errorf("live tool %v carries the withheld marker", tool["name"])
			}
		}
	}
}

// Calling a stub refuses with a structured payload and reaches no upstream.
// The spec points at a dead port, so a call that did reach out would hang or
// error differently.
func TestWithheldStubRefusesStructurally(t *testing.T) {
	ts := withheldServer(t, withheldSpec())
	defer ts.Close()

	call := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"edit_issue-comment","arguments":{}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, call).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true: the call did not do the thing", result["isError"])
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["error"] != withheldErrorCode {
		t.Errorf("error = %v, want %s", structured["error"], withheldErrorCode)
	}
	if structured["alternative"] != "create_thing" {
		t.Errorf("alternative = %v, want create_thing", structured["alternative"])
	}
	if reason, _ := structured["reason"].(string); !strings.Contains(reason, "audit-trail") {
		t.Errorf("reason = %v, want the stated reason", structured["reason"])
	}
}

// A stub with no alternative is legitimate - not every withheld verb has a
// substitute - and must not invent one.
func TestWithheldStubWithoutAlternative(t *testing.T) {
	spec := `withhold "delete_thing" {
    reason "Deletion is not offered on this surface at all."
}

` + roundTripSpec("http://127.0.0.1:1")
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tool := range s.tools {
		if tool.Name != "delete_thing" {
			continue
		}
		if strings.Contains(tool.Description, "instead") {
			t.Errorf("description = %q, want no alternative clause", tool.Description)
		}
		if _, ok := tool.GetMeta()[withheldMetaKey+"/alternative"]; ok {
			t.Error("_meta names an alternative that was never stated")
		}
		return
	}
	t.Fatal("delete_thing stub not minted")
}

func TestWithheldFailsClosed(t *testing.T) {
	for name, node := range map[string]string{
		"shadows a granted tool": `withhold "get_thing" { reason "no" }`,
		"missing reason":         `withhold "edit_thing" { alternative "get_thing" }`,
		"unknown alternative":    `withhold "edit_thing" { reason "no"; alternative "nope_thing" }`,
		"unknown child":          `withhold "edit_thing" { reason "no"; colour "red" }`,
		"property form":          `withhold "edit_thing" reason="no"`,
		"duplicate node":         "withhold \"edit_thing\" { reason \"a\" }\nwithhold \"edit_thing\" { reason \"b\" }",
		"duplicate reason":       `withhold "edit_thing" { reason "a"; reason "b" }`,
		"empty name":             `withhold "" { reason "no" }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("test", "test.mcp.kdl", []byte(node+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
			if err == nil {
				t.Fatal("New accepted a malformed `withhold` node")
			}
			if name == "shadows a granted tool" && !strings.Contains(err.Error(), "mints") {
				t.Errorf("error = %q, want it to name the shadowing", err)
			}
		})
	}
}

// A stub is part of the served surface, so lint must print it. A tool an agent
// sees but lint does not is the drift lint exists to catch.
func TestWithheldStubAppearsInToolNames(t *testing.T) {
	s, err := New("test", "test.mcp.kdl", []byte(withheldSpec()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !contains(s.ToolNames(), "edit_issue-comment") {
		t.Fatalf("ToolNames = %v, missing the served stub", s.ToolNames())
	}
	// It has no verb to resolve, so it must not claim a method.
	for _, m := range s.ToolMethods() {
		if m.Tool == "edit_issue-comment" {
			t.Errorf("stub reported a resolved method %q", m.Method)
		}
	}
	// It must still be separable from the info tool, which also resolves no
	// method for an entirely different reason.
	if got := s.WithheldTools(); len(got) != 1 || got[0] != "edit_issue-comment" {
		t.Errorf("WithheldTools = %v, want just the stub", got)
	}
}
