package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// inheritPair writes a parent guardfile under base/ and a child beside it, then
// returns the child's path. The parent sits in its own directory on purpose: a
// path it states must resolve against ITS directory, not the child's, and a
// flat layout cannot tell the two apart.
func inheritPair(t *testing.T, parentSiblings, parentWrap, child string) string {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	if err := os.MkdirAll(filepath.Join(base, "widgets"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "widgets", "w.html"), []byte(testWidget), 0o600); err != nil {
		t.Fatalf("write widget: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "parent.kdl"), []byte(parentSiblings+"\n"+parentWrap), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	path := filepath.Join(dir, "child.mcp.kdl")
	if err := os.WriteFile(path, []byte(child), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}
	return path
}

const parentWrapBody = `wrap ward mcp things {
    base-url "http://127.0.0.1:1"
    auth bearer { value env "MCP_BEAVER_TEST_TOKEN" }
    can get thing { path "/things/{id}" }
    can create thing { path "/things" }
}`

const bareChild = `wrap ward mcp things {
    inherit "base/parent.kdl"
    base-url "http://127.0.0.1:1"
    auth bearer { value env "MCP_BEAVER_TEST_TOKEN" }
}`

func inheritServer(t *testing.T, parentSiblings, child string) *Server {
	t.Helper()
	path := inheritPair(t, parentSiblings, parentWrapBody, child)
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	s, err := New("test", path, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// The security claim. A base tier states the gates, a child inherits the
// grants, and before #113 the child minted the tools with the gates silently
// gone - wider than its base, against the one invariant inherit holds.
func TestInheritCarriesTheParentsGates(t *testing.T) {
	siblings := `confirm "create_thing" message="Base says confirm this."

withhold "delete_thing" {
    reason "Deletion is withheld on this surface."
}`
	s := inheritServer(t, siblings, bareChild)

	withheld := s.WithheldTools()
	if len(withheld) != 1 || withheld[0] != "delete_thing" {
		t.Errorf("WithheldTools = %v, want the parent's stub carried down", withheld)
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	// Through the SDK client, so the confirmation is observed as the protocol
	// flow a caller meets rather than as a handler return value.
	asked := false
	session := connectSDKClient(t, ts, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = true
		if !strings.Contains(req.Params.Message, "Base says confirm this") {
			t.Errorf("elicit message = %q, want the parent's own text", req.Params.Message)
		}
		return &mcp.ElicitResult{Action: "decline"}, nil
	})
	_, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "create_thing",
		Arguments: map[string]any{"owner": "coilyco-x", "title": "t"},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !asked {
		// The upstream here is a dead port, so a missing gate would surface as
		// a connection error rather than a silent success. Assert the gate.
		t.Error("the tool ran without asking, so the parent's `confirm` did not carry down")
	}
}

// A widget named by a parent sits beside the PARENT. Resolving it against the
// child's directory finds nothing, which is why the chain carries a directory
// per guardfile rather than one path.
func TestInheritResolvesAParentsWidgetAgainstTheParent(t *testing.T) {
	siblings := `app "card" uri="ui://card/mcp-app.html" file="widgets/w.html" {
    tool "get_thing"
}`
	s := inheritServer(t, siblings, bareChild)
	if got := s.AppTools()["get_thing"]; got != "ui://card/mcp-app.html" {
		t.Fatalf("AppTools = %v, want the parent's widget linked", s.AppTools())
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	read := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"ui://card/mcp-app.html"}}`)
	var body struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, read).Result, &body); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if len(body.Contents) != 1 || body.Contents[0].Text != testWidget {
		t.Errorf("widget body = %v, want the file beside the parent", body.Contents)
	}
}

// A singleton is child-wins across the inherit edge, matching how `base-url`
// and `auth` already behave inside the wrap body. Stating your own
// instructions is not a collision with your base's.
func TestInheritSingletonIsChildWins(t *testing.T) {
	child := `instructions { text "The child speaks for itself." }

server-info name="child_status"

` + bareChild
	s := inheritServer(t, `instructions { text "Base tier instructions." }

server-info name="base_status"`, child)

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	var got struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &got); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	if !strings.Contains(got.Instructions, "The child speaks for itself") {
		t.Errorf("instructions = %q, want the child's", got.Instructions)
	}
	if strings.Contains(got.Instructions, "Base tier instructions") {
		t.Errorf("instructions = %q, want the base's replaced rather than appended", got.Instructions)
	}
	for _, name := range s.ToolNames() {
		if name == "base_status" {
			t.Error("ToolNames carries base_status, want the child's server-info name to win")
		}
	}
}

// Child-wins across files must not become last-wins inside one. Two
// `instructions` in a single guardfile is a typo, and it stays an error.
func TestInheritKeepsTheWithinFileDuplicateCheck(t *testing.T) {
	child := `instructions { text "First." }

instructions { text "Second." }

` + bareChild
	path := inheritPair(t, `instructions { text "Base." }`, parentWrapBody, child)
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	if _, err := New("test", path, src); err == nil || !strings.Contains(err.Error(), "duplicate `instructions`") {
		t.Errorf("err = %v, want the duplicate node still refused", err)
	}
}

// A child redefining a base's gate is a collision, not an override. Silently
// taking either one is how a weaker surface reads as the stronger one.
func TestInheritRefusesARedefinedGate(t *testing.T) {
	child := `confirm "create_thing" message="The child would rather not ask."

` + bareChild
	path := inheritPair(t, `confirm "create_thing" message="Base says confirm this."`, parentWrapBody, child)
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	if _, err := New("test", path, src); err == nil || !strings.Contains(err.Error(), "duplicate `confirm`") {
		t.Errorf("err = %v, want the redefinition refused", err)
	}
}

// umbra's own Flatten catches a cycle first and TestInheritCycleFailsClosed
// covers that path, so this exercises the sibling walk directly: it reads the
// disk on its own and would recurse forever on the guard's absence.
func TestInheritedSourcesRefusesACycle(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.mcp.kdl")
	b := filepath.Join(dir, "b.kdl")
	write := func(path, inherit string) {
		body := `wrap ward mcp things {
    inherit "` + inherit + `"
    base-url "http://127.0.0.1:1"
}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(a, "b.kdl")
	write(b, "a.mcp.kdl")
	src, err := os.ReadFile(a) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := inheritedSources(a, src); err == nil || !strings.Contains(err.Error(), "cycles back") {
		t.Errorf("err = %v, want the cycle named", err)
	}
}

// The chain is base-first and depth-first, so a parser sees a grandparent
// before a parent and a parent before the child. Order is what makes the
// singleton rule mean "nearest wins" rather than "some file wins".
func TestInheritedSourcesAreBaseFirst(t *testing.T) {
	dir := t.TempDir()
	wrapWith := func(inherit string) string {
		body := "wrap ward mcp things {\n"
		if inherit != "" {
			body += "    inherit \"" + inherit + "\"\n"
		}
		return body + "    base-url \"http://127.0.0.1:1\"\n}"
	}
	for name, inherit := range map[string]string{"grand.kdl": "", "mid.kdl": "grand.kdl", "child.mcp.kdl": "mid.kdl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("// "+name+"\n"+wrapWith(inherit)), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	path := filepath.Join(dir, "child.mcp.kdl")
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	sources, err := inheritedSources(path, src)
	if err != nil {
		t.Fatalf("inheritedSources: %v", err)
	}
	var order []string
	for _, source := range sources {
		order = append(order, strings.TrimPrefix(strings.SplitN(string(source.src), "\n", 2)[0], "// "))
	}
	want := []string{"grand.kdl", "mid.kdl", "child.mcp.kdl"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", order, want)
	}
}

const narrowAPI = `{"openapi":"3.0.0","info":{"title":"things","version":"1"},"paths":{"/things/{id}":{
"get":{"operationId":"getThing","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}},
"delete":{"operationId":"deleteThing","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`

// narrowingPair is spec mode, because the inline grammar has no `never` and a
// tier cannot subtract without one.
func narrowingPair(t *testing.T, parentSiblings, childBody string) (*Server, error) {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, at := range []string{filepath.Join(base, "api.json"), filepath.Join(dir, "api.json")} {
		if err := os.WriteFile(at, []byte(narrowAPI), 0o600); err != nil {
			t.Fatalf("write api: %v", err)
		}
	}
	parent := parentSiblings + `

wrap ward mcp things {
    spec api.json
    base-url "http://127.0.0.1:1"
    auth bearer { value env "MCP_BEAVER_TEST_TOKEN" }
    can get thing
    can delete thing
}`
	if err := os.WriteFile(filepath.Join(base, "parent.kdl"), []byte(parent), 0o600); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	path := filepath.Join(dir, "child.mcp.kdl")
	if err := os.WriteFile(path, []byte(childBody), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read child: %v", err)
	}
	return New("test", path, src)
}

const narrowingChild = `wrap ward mcp things {
    inherit "base/parent.kdl"
    spec api.json
    base-url "http://127.0.0.1:1"
    auth bearer { value env "MCP_BEAVER_TEST_TOKEN" }
    never delete thing
}`

// Carrying a base's gates down creates a dead end unless a vacated one is
// dropped: the base gates a tool, the child correctly removes that tool, and
// the child cannot build because it has no way to remove a gate on a tool it
// no longer has. The gate on the tool that SURVIVED must still bind, which is
// the half that makes this a drop rather than a hole.
func TestInheritDropsAControlVacatedByNarrowing(t *testing.T) {
	s, err := narrowingPair(t, `confirm "delete_thing" message="Base insists."
confirm "get_thing" message="Base gates the read too."`, narrowingChild)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	vacated := s.VacatedControls()
	if len(vacated) != 1 || vacated[0] != "delete_thing" {
		t.Errorf("VacatedControls = %v, want exactly delete_thing", vacated)
	}
	for _, name := range s.ToolNames() {
		if name == "delete_thing" {
			t.Fatal("delete_thing is minted, so this fixture is not testing narrowing")
		}
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	asked := false
	session := connectSDKClient(t, ts, func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked = true
		if !strings.Contains(req.Params.Message, "Base gates the read too") {
			t.Errorf("elicit message = %q, want the surviving gate's text", req.Params.Message)
		}
		return &mcp.ElicitResult{Action: "decline"}, nil
	})
	if _, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "get_thing",
		Arguments: map[string]any{"id": "1"},
	}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if !asked {
		t.Error("get_thing ran without asking, so dropping one gate dropped the other")
	}
}

// The drop is for a control an ANCESTOR stated. One this guardfile states
// itself on a tool it does not mint is a typo, and it stays an error.
func TestOwnControlOnAnUnmintedToolStaysAnError(t *testing.T) {
	child := `confirm "delete_thing" message="The child gates a tool it just removed."

` + narrowingChild
	_, err := narrowingPair(t, `instructions { text "Base." }`, child)
	if err == nil || !strings.Contains(err.Error(), "does not mint") {
		t.Errorf("err = %v, want the child's own control still refused", err)
	}
}
