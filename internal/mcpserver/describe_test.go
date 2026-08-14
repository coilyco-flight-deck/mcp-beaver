package mcpserver

import (
	"strings"
	"testing"
)

// describeSpec states two grants that a derived description cannot tell apart.
// This is the shape #58 was filed over: two book servers whose derived text
// read "Use this when the user wants to search book ..." and "Use this when the
// user wants to get readable-text ...", neither of which rules the other out.
func describeSpec() string {
	return `wrap ward mcp books {
    base-url "example.invalid/api/v1"
    auth bearer { value env "BOOKS_TOKEN" }
    can search book {
        path "/search.json"
        describe "Look up bibliographic detail for any book, in or out of print. Metadata and availability only - does NOT return readable text; use get_readable-text for that."
        query "q"
    }
    can get readable-text {
        path "/texts/{id}"
    }
}`
}

// An authored `describe` on a `can` grant reaches the served tool description.
// The field was unreachable from the wrap grammar when #58 was filed against
// cli-guard v0.131.0; umbra accepts it now, and this pins the whole path -
// parse, Descriptor.Describe, projection - rather than only the parse.
func TestGrantDescribeReachesToolDescription(t *testing.T) {
	s, err := New("books", "books.mcp.kdl", []byte(describeSpec()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var got string
	for _, tool := range s.tools {
		if tool.Name == "search_book" {
			got = tool.Description
		}
	}
	if got == "" {
		t.Fatal("search_book not minted")
	}
	if !strings.Contains(got, "does NOT return readable text") {
		t.Fatalf("description = %q, want the authored note", got)
	}
	if strings.Contains(got, "through the configured upstream service") {
		t.Errorf("description = %q, want the authored note to replace the derived string", got)
	}
}

// The fallback is what keeps every existing guardfile working: a grant with no
// `describe` still gets the derived sentence.
func TestGrantWithoutDescribeFallsBackToDerived(t *testing.T) {
	s, err := New("books", "books.mcp.kdl", []byte(describeSpec()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tool := range s.tools {
		if tool.Name != "get_readable-text" {
			continue
		}
		want := "Use this when the user wants to get readable-text through the configured upstream service."
		if tool.Description != want {
			t.Errorf("description = %q, want the derived %q", tool.Description, want)
		}
		return
	}
	t.Fatal("get_readable-text not minted")
}

// Fail-closed is the property that makes the note trustworthy: an empty or
// duplicated `describe` is an error, not a silent fallback to the derived text.
func TestGrantDescribeFailsClosed(t *testing.T) {
	for name, grant := range map[string]string{
		"empty note": `can search book {
        path "/search.json"
        describe ""
    }`,
		"duplicate note": `can search book {
        path "/search.json"
        describe "one"
        describe "two"
    }`,
	} {
		t.Run(name, func(t *testing.T) {
			spec := `wrap ward mcp books {
    base-url "example.invalid/api/v1"
    auth bearer { value env "BOOKS_TOKEN" }
    ` + grant + `
}`
			if _, err := New("books", "books.mcp.kdl", []byte(spec)); err == nil {
				t.Fatal("New accepted a malformed `describe`")
			}
		})
	}
}
