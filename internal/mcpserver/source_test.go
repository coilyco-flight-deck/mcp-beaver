package mcpserver

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// probeSwagger is a four-operation Swagger 2.0 slice: enough for the convention
// resolver to reach a collection, an item, a create, and a delete.
const probeSwagger = `{
  "swagger": "2.0",
  "info": {"title": "probe", "version": "1"},
  "basePath": "/api/v1",
  "paths": {
    "/things": {
      "get": {"operationId": "listThings", "responses": {"200": {"description": "ok"}}},
      "post": {"operationId": "createThing", "responses": {"201": {"description": "ok"}}}
    },
    "/things/{id}": {
      "get": {"operationId": "getThing", "parameters": [{"name": "id", "in": "path", "required": true, "type": "string"}], "responses": {"200": {"description": "ok"}}},
      "delete": {"operationId": "deleteThing", "parameters": [{"name": "id", "in": "path", "required": true, "type": "string"}], "responses": {"204": {"description": "ok"}}}
    }
  }
}`

// writeFixture drops one file into dir and returns its path.
func writeFixture(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// toolsOf builds the server and returns its sorted tool names.
func toolsOf(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	srv, err := New("probe", path, src)
	if err != nil {
		t.Fatalf("New(%s): %v", filepath.Base(path), err)
	}
	// mcp_beaver_info is the runtime's own tool, not a grant, so drop it.
	var names []string
	for _, n := range srv.ToolNames() {
		if n != "mcp_beaver_info" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// TestInlineGuardfileIsUnchanged pins the path every deployed spec takes today:
// no inherit and no spec node means the frozen inline grammar, untouched.
func TestInlineGuardfileIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := writeFixture(t, dir, "plain.mcp.kdl", `
wrap ward mcp probe {
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing {
        path "/things/{id}"
    }
}
`)
	if got, want := toolsOf(t, path), []string{"get_thing"}; !equalStrings(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

// TestInheritComposesTheInlineGrammar covers composition without a spec: a child
// gains its parent's grants, so a narrower sibling states only its difference.
func TestInheritComposesTheInlineGrammar(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "base.mcp.kdl", `
wrap ward mcp probe {
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing {
        path "/things/{id}"
    }
}
`)
	child := writeFixture(t, dir, "child.mcp.kdl", `
wrap ward mcp probe {
    inherit "base.mcp.kdl"
    can list thing {
        path "/things"
    }
}
`)
	got := toolsOf(t, child)
	want := []string{"get_thing", "list_thing"}
	if !equalStrings(got, want) {
		t.Errorf("tools = %v, want the parent's grant plus the child's %v", got, want)
	}
}

// TestSpecModeResolvesByConvention is the vocabulary half: the guardfile names a
// verb and a resource, and the spec supplies the path and method.
func TestSpecModeResolvesByConvention(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "probe.swagger.json", probeSwagger)
	path := writeFixture(t, dir, "spec.mcp.kdl", `
wrap ward mcp probe {
    spec probe.swagger.json
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing
    can list thing
}
`)
	got := toolsOf(t, path)
	want := []string{"get_thing", "list_thing"}
	if !equalStrings(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

// TestSpecModeNarrowsByInherit is the ladder: a tier states what it refuses and
// the denied leaf mints no tool at all, rather than a tool that refuses.
func TestSpecModeNarrowsByInherit(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "probe.swagger.json", probeSwagger)
	writeFixture(t, dir, "operator.kdl", `
wrap ward mcp probe {
    spec probe.swagger.json
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing
    can list thing
    can create thing
    can delete thing
}
`)
	tier := writeFixture(t, dir, "tier.mcp.kdl", `
wrap ward mcp probe {
    inherit "operator.kdl"
    never delete thing
    never create thing
}
`)
	got := toolsOf(t, tier)
	want := []string{"get_thing", "list_thing"}
	if !equalStrings(got, want) {
		t.Errorf("tools = %v, want the operator surface narrowed to %v", got, want)
	}
	for _, name := range got {
		if strings.HasPrefix(name, "delete_") || strings.HasPrefix(name, "create_") {
			t.Errorf("%q is served; a denied leaf must mint no tool", name)
		}
	}
}

// TestSpecModeRefusesToCrossAnInheritedDeny pins the escalation guard: widening a
// base tier has to be written by name, so it cannot happen by omission.
func TestSpecModeRefusesToCrossAnInheritedDeny(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "probe.swagger.json", probeSwagger)
	writeFixture(t, dir, "operator.kdl", `
wrap ward mcp probe {
    spec probe.swagger.json
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing
    never delete thing
}
`)
	tier := writeFixture(t, dir, "tier.mcp.kdl", `
wrap ward mcp probe {
    inherit "operator.kdl"
    can delete thing
}
`)
	src, err := os.ReadFile(tier)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err = New("probe", tier, src)
	if err == nil {
		t.Fatal("a bare `can` crossed an inherited `never`")
	}
	if !strings.Contains(err.Error(), "override") {
		t.Errorf("error = %v, want it to name the override escape hatch", err)
	}
}

// TestSpecModeReadsAGzippedSpec covers the vendored shape: the pruned Forgejo
// Swagger ships compressed so it fits a ConfigMap beside the guardfile.
func TestSpecModeReadsAGzippedSpec(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(probeSwagger)); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.swagger.json.gz"), buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write gz: %v", err)
	}
	path := writeFixture(t, dir, "spec.mcp.kdl", `
wrap ward mcp probe {
    spec probe.swagger.json.gz
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing
}
`)
	if got, want := toolsOf(t, path), []string{"get_thing"}; !equalStrings(got, want) {
		t.Errorf("tools = %v, want %v", got, want)
	}
}

// TestSpecModeConfinesTheSpecPath keeps a spec a sibling artifact, so a mounted
// guardfile cannot name a document outside the directory it was mounted with.
func TestSpecModeConfinesTheSpecPath(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "probe.swagger.json", probeSwagger)
	path := writeFixture(t, dir, "escape.mcp.kdl", `
wrap ward mcp probe {
    spec "../probe.swagger.json"
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing
}
`)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := New("probe", path, src); err == nil {
		t.Fatal("a spec path outside the guardfile's directory was accepted")
	}
}

// TestInheritCycleFailsClosed covers the composition's own footgun.
func TestInheritCycleFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "a.mcp.kdl", `
wrap ward mcp probe {
    inherit "b.mcp.kdl"
    can get thing {
        path "/things/{id}"
    }
}
`)
	b := writeFixture(t, dir, "b.mcp.kdl", `
wrap ward mcp probe {
    inherit "a.mcp.kdl"
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
}
`)
	src, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := New("probe", b, src); err == nil {
		t.Fatal("an inherit cycle was accepted")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
