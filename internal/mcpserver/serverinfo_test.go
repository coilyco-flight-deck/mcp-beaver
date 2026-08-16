package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func infoServer(t *testing.T, node string) *httptest.Server {
	t.Helper()
	s, err := New("test", "test.mcp.kdl", []byte(node+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return httptest.NewServer(s.Handler())
}

// On by default: a guardfile that says nothing gets the tool. Fleet
// consistency is the whole point - an agent can only read meaning from the
// tool's absence if absence means one thing everywhere.
func TestServerInfoMintedByDefault(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var found bool
	for _, tool := range toolList(t, decodeRPCResponse(t, resp)) {
		if tool["name"] == defaultServerInfoTool {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s not minted for a guardfile with no `server-info` node", defaultServerInfoTool)
	}
}

// The opt-out is the escape hatch that keeps the default honest: a deployment
// that genuinely wants the tool gone can still say so.
func TestServerInfoOptOut(t *testing.T) {
	ts := infoServer(t, "server-info disabled")
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for _, tool := range toolList(t, decodeRPCResponse(t, resp)) {
		if tool["name"] == defaultServerInfoTool {
			t.Fatalf("%s served despite `server-info disabled`", defaultServerInfoTool)
		}
	}
}

func TestServerInfoMintedWhenDeclared(t *testing.T) {
	ts := infoServer(t, "server-info")
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var found map[string]any
	for _, tool := range toolList(t, decodeRPCResponse(t, resp)) {
		if tool["name"] == defaultServerInfoTool {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("`server-info` declared but %s not served", defaultServerInfoTool)
	}
	annotations, _ := found["annotations"].(map[string]any)
	if annotations["readOnlyHint"] != true {
		t.Errorf("annotations = %v, want readOnlyHint true", annotations)
	}
	if annotations["openWorldHint"] != false {
		t.Errorf("annotations = %v, want openWorldHint false: it reaches no upstream", annotations)
	}

	call := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"`+defaultServerInfoTool+`","arguments":{}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, call).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["status"] != "ok" {
		t.Errorf("status = %v, want ok", structured["status"])
	}
	if structured["mode"] != "spec" {
		t.Errorf("mode = %v, want spec", structured["mode"])
	}
	// It counts itself: the payload must describe the surface actually served.
	if got := structured["toolCount"]; got != float64(3) {
		t.Errorf("toolCount = %v, want 3 (two grants plus the info tool)", got)
	}
}

func TestServerInfoHonoursNameOverride(t *testing.T) {
	ts := infoServer(t, `server-info name="status"`)
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var names []string
	for _, tool := range toolList(t, decodeRPCResponse(t, resp)) {
		names = append(names, tool["name"].(string))
	}
	if !contains(names, "status") {
		t.Fatalf("tools = %v, want the overridden name", names)
	}
	if contains(names, defaultServerInfoTool) {
		t.Errorf("tools = %v, want only the overridden name", names)
	}
}

// The info tool must appear in ToolNames, since `mcp-beaver lint` prints that as
// the surface a consumer diffs. A served-but-unlisted tool would under-report.
func TestServerInfoAppearsInToolNames(t *testing.T) {
	s, err := New("test", "test.mcp.kdl", []byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !contains(s.ToolNames(), defaultServerInfoTool) {
		t.Fatalf("ToolNames = %v, missing the served info tool", s.ToolNames())
	}
}

// Opting out must drop the tool from lint too, or lint and tools/list report
// different surfaces and the opt-out becomes invisible to a consumer.
func TestServerInfoOptOutLeavesToolNames(t *testing.T) {
	s, err := New("test", "test.mcp.kdl", []byte("server-info disabled\n\n"+roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if contains(s.ToolNames(), defaultServerInfoTool) {
		t.Fatalf("ToolNames = %v, still lists the opted-out info tool", s.ToolNames())
	}
}

func TestServerInfoFailsClosed(t *testing.T) {
	for name, node := range map[string]string{
		"collides with a grant": `server-info name="get_thing"`,
		"unknown property":      `server-info colour="red"`,
		"unknown argument":      `server-info "status"`,
		"too many arguments":    `server-info disabled extra`,
		"named while disabled":  `server-info disabled name="status"`,
		"empty name":            `server-info name=""`,
		"duplicate node":        "server-info\nserver-info",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("test", "test.mcp.kdl", []byte(node+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
			if err == nil {
				t.Fatal("New accepted a malformed `server-info` node")
			}
			if name == "collides with a grant" && !strings.Contains(err.Error(), "collides") {
				t.Errorf("error = %q, want it to name the collision", err)
			}
		})
	}
}

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}
