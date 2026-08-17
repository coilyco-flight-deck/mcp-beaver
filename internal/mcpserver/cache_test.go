package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cachedSpec grants one read and one write against baseURL, with the read
// cached. The write is present so a test can prove the cache is per tool.
func cachedSpec(baseURL, siblings string) string {
	return siblings + `
wrap ward mcp test {
    base-url "` + baseURL + `"
    auth bearer { value literal "unused" }
    can get thing {
        path "/things/{id}"
        query "q"
    }
    can create thing {
        path "/things"
        body { field "title" type="string" }
    }
}`
}

// tallyingUpstream answers every request and reports how many it received,
// which is the only number this feature exists to reduce. Distinct from
// confirm_test's countingUpstream, which is handler-shaped and cannot fail.
func tallyingUpstream(t *testing.T, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	calls := &atomic.Int64{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		if status != http.StatusOK {
			http.Error(w, "upstream is unhappy", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"call":` + strconv.FormatInt(n, 10) + `}`))
	}))
	t.Cleanup(ts.Close)
	return ts, calls
}

func serveSpec(t *testing.T, spec string) (*httptest.Server, string) {
	t.Helper()
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	return ts, initResp.Header.Get("Mcp-Session-Id")
}

func callTool(t *testing.T, ts *httptest.Server, sessionID, id, name, args string) map[string]any {
	t.Helper()
	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID,
		`{"jsonrpc":"2.0","id":`+id+`,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("tool result: %v", err)
	}
	return result
}

// The point of the whole feature: N identical questions from a community
// become one billable upstream call.
func TestCacheServesARepeatedCallWithoutReachingUpstream(t *testing.T) {
	upstream, calls := tallyingUpstream(t, http.StatusOK)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, `cache "get_thing" ttl="15m"`))

	first := callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42","q":"ramen"}`)
	second := callTool(t, ts, sessionID, "3", "get_thing", `{"id":"42","q":"ramen"}`)

	if got := calls.Load(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
	if firstText(t, first) != firstText(t, second) {
		t.Errorf("cache hit returned different content:\n %s\n %s", firstText(t, first), firstText(t, second))
	}
}

// Key on the request, not on the tool. A different question is a different
// answer, and a cache that confused the two would be worse than none.
func TestCacheKeysOnTheArguments(t *testing.T) {
	upstream, calls := tallyingUpstream(t, http.StatusOK)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, `cache "get_thing" ttl="15m"`))

	callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42","q":"ramen"}`)
	callTool(t, ts, sessionID, "3", "get_thing", `{"id":"42","q":"udon"}`)

	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 - two different questions", got)
	}
}

// Two callers spelling the same object in different key order must hit the
// same entry, or the cache silently does nothing for a JSON-emitting client.
func TestCacheKeyIsOrderIndependent(t *testing.T) {
	one, ok := cacheKey("get_thing", json.RawMessage(`{"id":"42","q":"ramen"}`))
	two, alsoOK := cacheKey("get_thing", json.RawMessage(`{"q":"ramen","id":"42"}`))
	if !ok || !alsoOK {
		t.Fatalf("cacheKey refused well-formed arguments")
	}
	if one != two {
		t.Fatalf("key order changed the cache key:\n %s\n %s", one, two)
	}
	if same, _ := cacheKey("create_thing", json.RawMessage(`{"id":"42","q":"ramen"}`)); same == one {
		t.Errorf("two different tools share a cache key: %s", same)
	}
}

// An upstream that 5xxed for fifteen seconds must not answer for the next
// fifteen minutes.
func TestCacheDoesNotStoreAFailedCall(t *testing.T) {
	upstream, calls := tallyingUpstream(t, http.StatusBadGateway)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, `cache "get_thing" ttl="15m"`))

	for _, id := range []string{"2", "3"} {
		result := callTool(t, ts, sessionID, id, "get_thing", `{"id":"42"}`)
		if isErr, _ := result["isError"].(bool); !isErr {
			t.Fatalf("call %s did not report the upstream failure: %v", id, result)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 - a failure must not be cached", got)
	}
}

// Off by default. A stale answer is worse than a slow one for anything
// time-sensitive, and no default can know which this is.
func TestUncachedToolStillReachesUpstreamEveryCall(t *testing.T) {
	upstream, calls := tallyingUpstream(t, http.StatusOK)
	ts, sessionID := serveSpec(t, cachedSpec(upstream.URL, ""))

	callTool(t, ts, sessionID, "2", "get_thing", `{"id":"42"}`)
	callTool(t, ts, sessionID, "3", "get_thing", `{"id":"42"}`)

	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2 - caching is opt-in", got)
	}
}

func TestCacheFailsClosedAtBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		siblings string
		want     string
	}{
		"unknown tool": {
			`cache "no_such_tool" ttl="15m"`,
			"not a grant-backed tool",
		},
		"info tool": {
			`cache "mcp_beaver_info" ttl="15m"`,
			"not a grant-backed tool",
		},
		"missing ttl": {
			`cache "get_thing"`,
			"needs a ttl",
		},
		"unparseable ttl": {
			`cache "get_thing" ttl="fifteen"`,
			"must be a duration",
		},
		"ttl over the ceiling": {
			`cache "get_thing" ttl="48h"`,
			"over the 24h0m0s ceiling",
		},
		"unknown property": {
			`cache "get_thing" window="15m"`,
			`unknown ` + "`cache`" + ` property "window"`,
		},
		"duplicate": {
			"cache \"get_thing\" ttl=\"15m\"\ncache \"get_thing\" ttl=\"5m\"",
			"duplicate `cache`",
		},
		"confirm-gated": {
			"confirm \"get_thing\"\ncache \"get_thing\" ttl=\"15m\"",
			"skip the human gate",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("test", "test.mcp.kdl", []byte(cachedSpec("http://127.0.0.1:1", tc.siblings)))
			if err == nil {
				t.Fatalf("New accepted %q", tc.siblings)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// Replaying a destructive call from a cache is not a saving, it is a lie about
// what happened upstream.
func TestCacheRejectsADestructiveGrant(t *testing.T) {
	spec := `cache "delete_thing" ttl="15m"
wrap ward mcp test {
    base-url "http://127.0.0.1:1"
    auth bearer { value literal "unused" }
    can delete thing {
        path "/things/{id}"
    }
}`
	_, err := New("test", "test.mcp.kdl", []byte(spec))
	if err == nil {
		t.Fatalf("New cached a destructive grant")
	}
	if !strings.Contains(err.Error(), "which is destructive") {
		t.Fatalf("error = %q, want it to name the destructive grant", err)
	}
}

func TestResponseCacheExpiresAndStaysBounded(t *testing.T) {
	// Fixed rather than time.Now(), so the assertions are about the ttl
	// arithmetic and not about how long the test took.
	base := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	c := newResponseCache(time.Minute)
	c.put("k", &mcp.CallToolResult{}, base)

	if _, found := c.get("k", base.Add(59*time.Second)); !found {
		t.Errorf("entry expired before its ttl")
	}
	if _, found := c.get("k", base.Add(time.Minute)); found {
		t.Errorf("entry survived its ttl")
	}

	// Every entry still live, so purging cannot help and the hard cap has to.
	c = newResponseCache(time.Hour)
	for i := range maxCacheEntries + 50 {
		c.put(strconv.Itoa(i), &mcp.CallToolResult{}, base)
	}
	if got := len(c.entries); got > maxCacheEntries {
		t.Fatalf("cache holds %d entries, over the %d cap", got, maxCacheEntries)
	}
}
