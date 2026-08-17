package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// searchOverPOSTSpec is the Exa shape: a pure read served over POST. The verb
// used to be the only lever on the method, so reaching POST meant naming the
// tool `create_web_search` - a name that says it creates something, for a call
// that creates nothing (mcp-beaver#72).
//
// The body `map` is the other half of the same upstream: Exa takes its search
// string in a key literally named `query`, which is a reserved engine flag, so
// the mapping renames it the way `upstream=` renames a query parameter (#66).
func searchOverPOSTSpec(baseURL string) string {
	return `wrap ward mcp exa {
    base-url "` + baseURL + `"
    auth bearer { value literal "unused" }
    can search web {
        method "POST"
        path "/search"
        body {
            map "search" to="query"
        }
    }
}`
}

func TestGrantStatesItsOwnMethod(t *testing.T) {
	var gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer upstream.Close()

	s, err := New("exa", "exa.mcp.kdl", []byte(searchOverPOSTSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The name is the point. `search web` used to force GET, so the only way
	// to POST was a create-shaped verb, and a model pattern-matches hardest on
	// exactly this string.
	names := s.ToolNames()
	if !contains(names, "search_web") {
		t.Fatalf("tools = %v, want search_web", names)
	}
	if contains(names, "create_web_search") {
		t.Fatalf("tools = %v, still carrying the create-shaped workaround", names)
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")

	result := callTool(t, ts, sessionID, "2", "search_web", `{"search":"official kubernetes docs"}`)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("search_web reported isError; content=%v", result["content"])
	}
	if gotMethod != http.MethodPost {
		t.Errorf("upstream method = %q, want POST", gotMethod)
	}
	if gotPath != "/search" {
		t.Errorf("upstream path = %q, want /search", gotPath)
	}
}

// A stated method is a decision, so it is owed no fallthrough warning. The old
// check re-derived the answer from the verb and would have warned anyway,
// which would have left the 20-line comment in the guardfile explaining a
// warning instead of a name.
func TestStatedMethodSuppressesTheFallthroughWarning(t *testing.T) {
	s, err := New("exa", "exa.mcp.kdl", []byte(searchOverPOSTSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, m := range s.ToolMethods() {
		if m.Tool != "search_web" {
			continue
		}
		if m.Fallthrough {
			t.Errorf("search_web reported as a fallthrough despite stating `method \"POST\"`")
		}
		if m.Method != http.MethodPost {
			t.Errorf("search_web method = %q, want POST", m.Method)
		}
		return
	}
	t.Fatalf("ToolMethods did not report search_web: %v", s.ToolMethods())
}

// The verb table stays the default, so nothing existing changes.
func TestVerbStillPicksTheMethodWhenNoneIsStated(t *testing.T) {
	spec := `wrap ward mcp test {
    base-url "http://127.0.0.1:1"
    auth bearer { value literal "unused" }
    can search thing {
        path "/things"
        query "q"
    }
}`
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, m := range s.ToolMethods() {
		if m.Tool == "search_thing" && m.Method != http.MethodGet {
			t.Fatalf("search_thing method = %q, want GET from the verb table", m.Method)
		}
	}
}

func TestGrantRejectsANonMethod(t *testing.T) {
	spec := strings.Replace(searchOverPOSTSpec("http://127.0.0.1:1"), `method "POST"`, `method "FETCH"`, 1)
	_, err := New("exa", "exa.mcp.kdl", []byte(spec))
	if err == nil {
		t.Fatalf("New accepted a non-method")
	}
	if !strings.Contains(err.Error(), "is not an HTTP method") {
		t.Fatalf("error = %q, want it to name the invalid method", err)
	}
}
