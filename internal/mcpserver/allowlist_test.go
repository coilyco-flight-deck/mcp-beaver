package mcpserver

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestValidateAllowlistAcceptsAndTrims(t *testing.T) {
	got, err := ValidateAllowlist([]string{" signoz_list_metrics ", "signoz_get_alert"})
	if err != nil {
		t.Fatalf("ValidateAllowlist: %v", err)
	}
	want := []string{"signoz_list_metrics", "signoz_get_alert"}
	if !slices.Equal(got, want) {
		t.Fatalf("ValidateAllowlist = %v, want %v (trimmed, original order)", got, want)
	}
}

func TestValidateAllowlistRejectsMalformedEntries(t *testing.T) {
	for name, tc := range map[string]struct {
		tools []string
		want  string
	}{
		"empty list":       {nil, "allowlist is empty"},
		"empty name":       {[]string{"a", ""}, "entry 2 is empty"},
		"whitespace name":  {[]string{"   "}, "entry 1 is empty"},
		"duplicate":        {[]string{"a", "a"}, `duplicate upstream tool allowlist entry "a"`},
		"padded duplicate": {[]string{"a", " a "}, `duplicate upstream tool allowlist entry "a"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ValidateAllowlist(tc.tools)
			if err == nil {
				t.Fatalf("ValidateAllowlist accepted %v", tc.tools)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// The whole point of the heuristic is that it reads name segments rather than
// substrings, so a read-only name that merely contains a verb stays clear.
func TestMutationSuspectsSegmentsNames(t *testing.T) {
	allowlist := []string{
		"signoz_get_field_values",
		"signoz_check_metric_usage",
		"signoz_list_dashboards",
		"getSubredditRss",
		"signoz_create_dashboard",
		"signoz_update_alert",
		"deleteThing",
		"assets.upload",
	}
	got := MutationSuspects(allowlist)
	want := []string{"deleteThing", "signoz_create_dashboard", "signoz_update_alert"}
	if !slices.Equal(got, want) {
		t.Fatalf("MutationSuspects = %v, want %v", got, want)
	}
}

// "set" inside get_field_values and "put" inside compute are the substring
// false positives a naive strings.Contains check would raise.
func TestMutationSuspectsIgnoresSubstringMatches(t *testing.T) {
	for _, name := range []string{
		"get_field_values",
		"list_compute_nodes",
		"get_asset_metadata",
		"search_updates_feed",
	} {
		if got := MutationSuspects([]string{name}); len(got) > 0 {
			t.Errorf("MutationSuspects(%q) = %v, want none", name, got)
		}
	}
}

func annotatedUpstream(t *testing.T, readOnly map[string]bool) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "upstream", Version: "0.1.0"}, nil)
	names := make([]string, 0, len(readOnly))
	for name := range readOnly {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		tool := &mcp.Tool{
			Name:        name,
			Description: name,
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		}
		if readOnly[name] {
			tool.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
		}
		mcp.AddTool(srv, tool, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	}
	return srv
}

// NotReadOnly is what makes `lint-upstream --read-only strict` authoritative
// rather than a name guess: it reports what upstream actually annotated.
func TestNotReadOnlyReportsUnannotatedTools(t *testing.T) {
	upstreamTS := newUpstreamServer(t, annotatedUpstream(t, map[string]bool{
		"read_one":   true,
		"read_two":   true,
		"mutate_one": false,
	}))
	defer upstreamTS.Close()

	srv, err := NewProxy(
		context.Background(), "proxy", "", upstreamTS.URL+"/mcp",
		[]string{"read_one", "read_two", "mutate_one"}, upstreamTS.Client(),
	)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if got, want := srv.NotReadOnly(), []string{"mutate_one"}; !slices.Equal(got, want) {
		t.Fatalf("NotReadOnly = %v, want %v", got, want)
	}
}

func TestNotReadOnlyEmptyWhenEveryToolIsAnnotated(t *testing.T) {
	upstreamTS := newUpstreamServer(t, annotatedUpstream(t, map[string]bool{
		"read_one": true,
		"read_two": true,
	}))
	defer upstreamTS.Close()

	srv, err := NewProxy(
		context.Background(), "proxy", "", upstreamTS.URL+"/mcp",
		[]string{"read_one", "read_two"}, upstreamTS.Client(),
	)
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })

	if got := srv.NotReadOnly(); len(got) > 0 {
		t.Fatalf("NotReadOnly = %v, want none", got)
	}
}
