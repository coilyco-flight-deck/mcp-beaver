package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// #53 asks for `create_issue` to carry labels so filing is one call rather
// than 2N, and wonders whether it needs new plumbing for array-valued body
// fields. It does not: typed body arrays already work. This pins the exact
// grant shape the deploy-side guardfile needs, so the request is a one-line
// guardfile edit rather than a runtime change.
//
// items="integer" rather than "string" on purpose. Forgejo's CreateIssueOption
// takes label IDs, and deploy's existing add_issue-label grant already carries
// a note that declaring strings made numeric ids arrive quoted and mutate
// nothing. Repeating that mistake here would ship a labels parameter that
// silently labelled nothing, which is worse than the two-call sequence.
const createIssueWithLabelsSpec = `wrap ward mcp forgejo {
    base-url "%s"
    auth bearer { value env "FORGEJO_TOKEN" }
    can create issue {
        path "/repos/{owner}/{repo}/issues"
        body {
            field "title" type="string" required=#true
            field "body" type="string"
            array "labels" items="integer"
        }
    }
}`

func TestCreateIssueAcceptsLabelArray(t *testing.T) {
	t.Setenv("FORGEJO_TOKEN", "test-token")

	var got map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"number":1}`))
	}))
	defer upstream.Close()

	spec := []byte(fmt.Sprintf(createIssueWithLabelsSpec, upstream.URL))
	s, err := New("forgejo", "forgejo.mcp.kdl", spec)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The schema has to advertise the array, or no model will ever send one.
	var schema map[string]any
	for _, tool := range s.tools {
		if tool.Name == "create_issue" {
			if err := json.Unmarshal(tool.InputSchema.(json.RawMessage), &schema); err != nil {
				t.Fatalf("schema: %v", err)
			}
		}
	}
	props, _ := schema["properties"].(map[string]any)
	labels, _ := props["labels"].(map[string]any)
	if labels["type"] != "array" {
		t.Fatalf("labels schema = %v, want an array", labels)
	}
	items, _ := labels["items"].(map[string]any)
	if items["type"] != "integer" {
		t.Errorf("labels items = %v, want integer ids", items)
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_issue","arguments":{"owner":"coilyco-flight-deck","repo":"mcp-beaver","title":"t","labels":[3,7]}}}`)

	// The ids must arrive as JSON numbers. Quoted ids are the failure deploy
	// already hit once on add_issue-label: accepted, and silently inert.
	want := []any{float64(3), float64(7)}
	if !reflect.DeepEqual(got["labels"], want) {
		t.Errorf("upstream body labels = %#v, want %#v", got["labels"], want)
	}
	if got["title"] != "t" {
		t.Errorf("upstream body title = %v, want t", got["title"])
	}
}
