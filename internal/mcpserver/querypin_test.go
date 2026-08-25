package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// pinnedSpec is the Steam shape from mcp-beaver#52: an account-scoped read
// whose scope is a query parameter rather than part of the verb.
const pinnedSpec = `pin "get_owned_games" {
    query "steamid" env "STEAM_STEAMID64"
    query "include_appinfo" literal "1"
}

wrap ward mcp steam {
    base-url "%s"
    auth query-param {
        param "key" { value env "STEAM_API_KEY" }
    }
    can get owned_games {
        path "/IPlayerService/GetOwnedGames/v1/"
    }
}`

func pinnedServer(t *testing.T, spec string) (*httptest.Server, func() url.Values) {
	t.Helper()
	var mu sync.Mutex
	var seen url.Values
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = r.URL.Query()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"game_count":2}}`))
	}))
	t.Cleanup(upstream.Close)

	s, err := New("steam", "steam.mcp.kdl", []byte(fmt.Sprintf(spec, upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, func() url.Values {
		mu.Lock()
		defer mu.Unlock()
		return seen
	}
}

// The pinned scope reaches the upstream even though the caller sent nothing.
func TestQueryPinReachesUpstream(t *testing.T) {
	t.Setenv("STEAM_API_KEY", "test-key")
	t.Setenv("STEAM_STEAMID64", "76561190000000000")
	ts, seen := pinnedServer(t, pinnedSpec)

	postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_owned_games","arguments":{}}}`)

	got := seen()
	if got.Get("steamid") != "76561190000000000" {
		t.Errorf("steamid = %q, want the pinned value", got.Get("steamid"))
	}
	if got.Get("include_appinfo") != "1" {
		t.Errorf("include_appinfo = %q, want the pinned literal", got.Get("include_appinfo"))
	}
	// auth query-param still applies alongside the pins.
	if got.Get("key") != "test-key" {
		t.Errorf("key = %q, want the resolved credential", got.Get("key"))
	}
}

// The whole point: a caller cannot substitute its own scope - this is
// "anyone's library" versus "Kai's library". The pinned name is absent from the
// tool schema, so supplying it is now refused outright rather than dropped and
// overruled. Both hold the scope; refusing also stops the caller being told its
// request succeeded when a different one ran (mcp-beaver#94).
func TestQueryPinIsNotCallerSupplied(t *testing.T) {
	t.Setenv("STEAM_API_KEY", "test-key")
	t.Setenv("STEAM_STEAMID64", "76561190000000000")
	ts, seen := pinnedServer(t, pinnedSpec)

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_owned_games","arguments":{"steamid":"76561199999999999"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("supplying a pinned parameter must be refused, got: %v", result)
	}
	if got := seen().Get("steamid"); got == "76561199999999999" {
		t.Fatalf("steamid = %q: a caller overrode the server-side scope", got)
	}
}

// A call that does not contest the pin still gets the pinned scope.
func TestQueryPinAppliesWhenUncontested(t *testing.T) {
	t.Setenv("STEAM_API_KEY", "test-key")
	t.Setenv("STEAM_STEAMID64", "76561190000000000")
	ts, seen := pinnedServer(t, pinnedSpec)

	postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_owned_games","arguments":{}}}`)

	if got := seen().Get("steamid"); got != "76561190000000000" {
		t.Fatalf("steamid = %q, want the pinned value", got)
	}
}

// A pinned parameter must not appear in the tool schema, or a model will try
// to fill it and be silently ignored - which reads as the tool misbehaving.
func TestQueryPinIsAbsentFromSchema(t *testing.T) {
	t.Setenv("STEAM_API_KEY", "test-key")
	t.Setenv("STEAM_STEAMID64", "76561190000000000")
	s, err := New("steam", "steam.mcp.kdl", []byte(fmt.Sprintf(pinnedSpec, "http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tool := range s.tools {
		if tool.Name != "get_owned_games" {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema.(json.RawMessage), &schema); err != nil {
			t.Fatalf("schema: %v", err)
		}
		props, _ := schema["properties"].(map[string]any)
		for _, pinned := range []string{"steamid", "include_appinfo"} {
			if _, present := props[pinned]; present {
				t.Errorf("schema advertises pinned parameter %q", pinned)
			}
		}
		return
	}
	t.Fatal("get_owned_games not minted")
}

// An unresolvable pin fails the call rather than quietly sending an unscoped
// request. Silently dropping the scope is the dangerous direction.
func TestQueryPinFailsCallWhenUnresolvable(t *testing.T) {
	t.Setenv("STEAM_API_KEY", "test-key")
	// STEAM_STEAMID64 deliberately unset.
	ts, seen := pinnedServer(t, pinnedSpec)

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_owned_games","arguments":{}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("call result: %v", err)
	}
	if result["isError"] != true {
		t.Errorf("isError = %v, want true when a pin cannot resolve", result["isError"])
	}
	if len(seen()) != 0 {
		t.Error("an unscoped request reached the upstream after a pin failed to resolve")
	}
}

func TestQueryPinFailsClosed(t *testing.T) {
	base := `
wrap ward mcp steam {
    base-url "http://127.0.0.1:1"
    auth bearer { value env "T" }
    can get owned_games {
        path "/a"
        query "region"
    }
}`
	for name, node := range map[string]string{
		"unminted tool":       `pin "get_nothing" { query "steamid" literal "1" }`,
		"collides with field": `pin "get_owned_games" { query "region" literal "eu" }`,
		"unknown provider":    `pin "get_owned_games" { query "steamid" ssm "/a/b" }`,
		"unknown child":       `pin "get_owned_games" { header "x" literal "1" }`,
		"no children":         `pin "get_owned_games" { }`,
		"wrong arity":         `pin "get_owned_games" { query "steamid" literal }`,
		"empty source":        `pin "get_owned_games" { query "steamid" literal "" }`,
		"duplicate pin node":  "pin \"get_owned_games\" { query \"a\" literal \"1\" }\npin \"get_owned_games\" { query \"b\" literal \"2\" }",
		"duplicate query":     `pin "get_owned_games" { query "a" literal "1"; query "a" literal "2" }`,
		"property form":       `pin "get_owned_games" steamid="1"`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("steam", "steam.mcp.kdl", []byte(node+"\n"+base))
			if err == nil {
				t.Fatal("New accepted a malformed `pin` node")
			}
			if name == "collides with field" && !strings.Contains(err.Error(), "caller input") {
				t.Errorf("error = %q, want it to name the collision", err)
			}
		})
	}
}

// A guardfile with no pins is untouched.
func TestQueryPinAbsentByDefault(t *testing.T) {
	pins, err := parseQueryPins([]byte(roundTripSpec("http://127.0.0.1:1")))
	if err != nil {
		t.Fatalf("parseQueryPins: %v", err)
	}
	if pins != nil {
		t.Error("a guardfile with no `pin` node got pins")
	}
}
