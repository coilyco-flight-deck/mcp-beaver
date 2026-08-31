package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const testWidget = "<!doctype html><title>Thing</title><p>widget"

// appSpecDir writes a guardfile and its widget into one directory, because the
// `app` node resolves `file` against the guardfile's own directory and a test
// that fakes that path proves nothing about the runtime.
func appSpecDir(t *testing.T, appNode, widget string) string {
	t.Helper()
	dir := t.TempDir()
	if widget != "" {
		if err := os.WriteFile(filepath.Join(dir, "widget.html"), []byte(widget), 0o600); err != nil {
			t.Fatalf("write widget: %v", err)
		}
	}
	path := filepath.Join(dir, "test.mcp.kdl")
	src := appNode + "\n\n" + roundTripSpec("http://127.0.0.1:1")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return path
}

func appServer(t *testing.T, appNode string) *Server {
	t.Helper()
	path := appSpecDir(t, appNode, testWidget)
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	s, err := New("test", path, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

const basicApp = `app "thing-card" uri="ui://thing-card/mcp-app.html" file="widget.html" {
    description "The thing, as a card"
    tool "get_thing"
}`

// The two halves a host needs are on two different messages, and neither is
// visible from the parsed struct: the link rides on tools/list and the bytes
// ride on resources/read. Assert the wire on both, because a widget that
// serves correctly with the link missing is fetched by nobody and lints
// identically to a working one.
func TestAppLinkAndWidgetReachTheWire(t *testing.T) {
	ts := httptest.NewServer(appServer(t, basicApp).Handler())
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	var list struct {
		Tools []struct {
			Name string         `json:"name"`
			Meta map[string]any `json:"_meta"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &list); err != nil {
		t.Fatalf("list result: %v", err)
	}
	var linked, unlinked int
	for _, tool := range list.Tools {
		if tool.Name != "get_thing" {
			if _, has := tool.Meta["ui"]; !has {
				unlinked++
			}
			continue
		}
		linked++
		ui, _ := tool.Meta["ui"].(map[string]any)
		if ui["resourceUri"] != "ui://thing-card/mcp-app.html" {
			t.Errorf("_meta.ui = %v, want the declared resourceUri", tool.Meta["ui"])
		}
		// The published reference servers emit the flattened spelling BESIDE
		// the nested object rather than instead of it, so a host reading
		// either era finds the link. Measured on server-basic-vanillajs and
		// server-map, 2026-08-30.
		if tool.Meta["ui/resourceUri"] != "ui://thing-card/mcp-app.html" {
			t.Errorf("_meta[\"ui/resourceUri\"] = %v, want the same uri flattened", tool.Meta["ui/resourceUri"])
		}
	}
	if linked != 1 {
		t.Fatalf("linked tools = %d, want exactly get_thing", linked)
	}
	// Not every tool carries a widget, and a host decides per call. A change
	// that linked the whole surface would still pass the assertions above.
	if unlinked == 0 {
		t.Error("every tool carries a widget, want the link only on the one declared")
	}

	read := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ui://thing-card/mcp-app.html"}}`)
	var body struct {
		Contents []struct {
			MIMEType string `json:"mimeType"`
			Text     string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, read).Result, &body); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if len(body.Contents) != 1 {
		t.Fatalf("contents = %v", body.Contents)
	}
	// A host matches on this string to tell a widget from reference text, so
	// it is the whole difference between rendering and not.
	if body.Contents[0].MIMEType != appMIMEType {
		t.Errorf("mimeType = %q, want %q", body.Contents[0].MIMEType, appMIMEType)
	}
	if body.Contents[0].Text != testWidget {
		t.Errorf("text = %q, want the file byte for byte", body.Contents[0].Text)
	}
}

// csp, permissions, domain, and prefersBorder ride on the CONTENT item of
// resources/read, not on the tool and not on the resources/list entry. The
// spec marks csp and permissions `never` on the tool, and the published
// server-map was measured putting its csp exactly here.
func TestAppUIMetaRidesOnTheContentItem(t *testing.T) {
	spec := `app "map" uri="ui://map/mcp-app.html" file="widget.html" domain="a904.example.com" prefers-border=#false {
    tool "get_thing" visibility="model app"
    csp {
        connect "https://*.cesium.com"
        resource "https://*.cesium.com" "https://cdn.example.com"
        frame "https://player.vimeo.com"
        base-uri "https://cdn.example.com"
    }
    permission "clipboardWrite" "geolocation"
}`
	ts := httptest.NewServer(appServer(t, spec).Handler())
	defer ts.Close()

	read := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ui://map/mcp-app.html"}}`)
	var body struct {
		Contents []struct {
			Meta map[string]any `json:"_meta"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, read).Result, &body); err != nil {
		t.Fatalf("read result: %v", err)
	}
	ui, _ := body.Contents[0].Meta["ui"].(map[string]any)
	if ui == nil {
		t.Fatalf("_meta = %v, want a ui block", body.Contents[0].Meta)
	}
	if ui["domain"] != "a904.example.com" {
		t.Errorf("domain = %v", ui["domain"])
	}
	// false is the point: omitted leaves the border to the host, and stating
	// it is what the spec recommends, so a false must survive the wire rather
	// than being dropped as a zero value.
	if border, ok := ui["prefersBorder"].(bool); !ok || border {
		t.Errorf("prefersBorder = %v, want an explicit false", ui["prefersBorder"])
	}
	csp, _ := ui["csp"].(map[string]any)
	for field, want := range map[string]int{"connectDomains": 1, "resourceDomains": 2, "frameDomains": 1, "baseUriDomains": 1} {
		got, _ := csp[field].([]any)
		if len(got) != want {
			t.Errorf("csp.%s = %v, want %d origin(s)", field, csp[field], want)
		}
	}
	perms, _ := ui["permissions"].(map[string]any)
	for _, want := range []string{"clipboardWrite", "geolocation"} {
		if _, ok := perms[want]; !ok {
			t.Errorf("permissions = %v, want %s present", perms, want)
		}
	}
	if _, unwanted := perms["camera"]; unwanted {
		t.Errorf("permissions = %v, want only what was declared", perms)
	}

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var list struct {
		Tools []struct {
			Name string         `json:"name"`
			Meta map[string]any `json:"_meta"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &list); err != nil {
		t.Fatalf("list result: %v", err)
	}
	for _, tool := range list.Tools {
		if tool.Name != "get_thing" {
			continue
		}
		ui, _ := tool.Meta["ui"].(map[string]any)
		if _, leaked := ui["csp"]; leaked {
			t.Errorf("tool _meta.ui = %v, want no csp: hosts ignore it there", ui)
		}
		if _, leaked := ui["permissions"]; leaked {
			t.Errorf("tool _meta.ui = %v, want no permissions there", ui)
		}
		visibility, _ := ui["visibility"].([]any)
		if len(visibility) != 2 || visibility[0] != "model" || visibility[1] != "app" {
			t.Errorf("visibility = %v, want [model app] in the declared order", ui["visibility"])
		}
	}
}

// An `app` link merges into the tool's existing `_meta` rather than replacing
// it. A `withhold` stub writes its own marker there, and losing it would turn
// a refusing stub back into a tool a client reads as ordinary.
func TestAppMetaMergesRatherThanReplaces(t *testing.T) {
	tool := &mcp.Tool{Name: "get_thing"}
	tool.SetMeta(map[string]any{withheldMetaKey: true})
	applyAppMeta(map[string]map[string]any{"get_thing": {uiMetaKey: map[string]any{"resourceUri": "ui://x"}}}, tool)
	if tool.GetMeta()[withheldMetaKey] != true {
		t.Errorf("_meta = %v, want the pre-existing marker kept", tool.GetMeta())
	}
	if tool.GetMeta()[uiMetaKey] == nil {
		t.Errorf("_meta = %v, want the widget link added", tool.GetMeta())
	}
}

// Every refusal here is a widget that would otherwise serve cleanly and render
// for nobody, which is the failure this node exists to make loud.
func TestAppFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{"unknown property", `app "a" uri="ui://a" file="widget.html" colour="red" { tool "get_thing" }`, "unknown `app` property"},
		{"unknown child", `app "a" uri="ui://a" file="widget.html" { audience "assistant"; tool "get_thing" }`, "unknown `app` child"},
		{"wrong scheme", `app "a" uri="beaver://a" file="widget.html" { tool "get_thing" }`, "needs a uri under ui://"},
		{"no file", `app "a" uri="ui://a" { tool "get_thing" }`, "needs a non-empty file"},
		{"absolute file", `app "a" uri="ui://a" file="/etc/hosts" { tool "get_thing" }`, "must be relative to the guardfile"},
		{"missing file", `app "a" uri="ui://a" file="absent.html" { tool "get_thing" }`, "absent.html"},
		{"no tool", `app "a" uri="ui://a" file="widget.html" { description "x" }`, "names no `tool`"},
		{"unminted tool", `app "a" uri="ui://a" file="widget.html" { tool "delete_thing" }`, "does not mint"},
		{"bad visibility", `app "a" uri="ui://a" file="widget.html" { tool "get_thing" visibility="everyone" }`, "is not model or app"},
		{"bad permission", `app "a" uri="ui://a" file="widget.html" { tool "get_thing"; permission "filesystem" }`, "is not camera"},
		{"bad csp child", `app "a" uri="ui://a" file="widget.html" { tool "get_thing"; csp { script "https://x" } }`, "csp child"},
		{"duplicate uri", `app "a" uri="ui://a" file="widget.html" { tool "get_thing" }
app "b" uri="ui://a" file="widget.html" { tool "create_thing" }`, "duplicate `app` uri"},
		{"two apps one tool", `app "a" uri="ui://a" file="widget.html" { tool "get_thing" }
app "b" uri="ui://b" file="widget.html" { tool "get_thing" }`, "claimed by both"},
		{"uri taken by a resource", `resource "r" uri="ui://a" { text "x" }
app "a" uri="ui://a" file="widget.html" { tool "get_thing" }`, "already served by a `resource`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := appSpecDir(t, tc.spec, testWidget)
			src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
			if err != nil {
				t.Fatalf("read spec: %v", err)
			}
			_, err = New("test", path, src)
			if err == nil {
				t.Fatalf("New succeeded, want a refusal naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// A widget is not pulled into a model's context, it is fetched by a host
// following a tool link, so the `audience` warning must not fire on one. An
// author has no correct answer to give it.
func TestAppIsNotReportedByTheAudienceLint(t *testing.T) {
	s := appServer(t, basicApp)
	for _, name := range s.ResourcesWithoutAudience() {
		if name == "thing-card" {
			t.Errorf("ResourcesWithoutAudience = %v, want the widget excluded", s.ResourcesWithoutAudience())
		}
	}
	if s.AppTools()["get_thing"] != "ui://thing-card/mcp-app.html" {
		t.Errorf("AppTools = %v, want get_thing mapped to its widget", s.AppTools())
	}
}
