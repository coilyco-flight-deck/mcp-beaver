package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func bodyMappingSpec(baseURL string) string {
	return `wrap ward mcp webhook {
    base-url "` + baseURL + `"
    auth bearer { value literal "test-token" }
    can create message {
        path "/messages"
        body {
            map "commonAnnotations.summary" to="text"
        }
    }
}`
}

func TestBodyMappingProjectsSameRequestThroughMCPAndHTTP(t *testing.T) {
	var gotBodies []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		gotBodies = append(gotBodies, body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	s, err := New("mapper", "mapper.mcp.kdl", []byte(bodyMappingSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	arguments := map[string]any{
		"commonAnnotations": map[string]any{
			"summary": "API error rate is high",
			"secret":  "must-not-leak",
		},
		"ignored": "must-not-leak",
	}
	rawArguments, err := json.Marshal(arguments)
	if err != nil {
		t.Fatalf("marshal arguments: %v", err)
	}

	apiResp := postAPI(t, ts.Client(), ts.URL+"/api/create_message", "application/json", string(rawArguments))
	if apiResp.StatusCode != http.StatusOK {
		t.Fatalf("API status = %d, want 200; body = %v", apiResp.StatusCode, decodeAPIResult(t, apiResp))
	}
	_ = decodeAPIResult(t, apiResp)

	initResp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	sessionID := initResp.Header.Get("Mcp-Session-Id")
	if out := decodeRPCResponse(t, initResp); out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}
	call, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "create_message",
			"arguments": arguments,
		},
	})
	if err != nil {
		t.Fatalf("marshal tools/call: %v", err)
	}
	callResp := decodeRPCResponse(t, postToServer(t, ts.Client(), ts.URL+"/mcp", sessionID, string(call)))
	if callResp.Error != nil {
		t.Fatalf("tools/call error: %+v", callResp.Error)
	}
	var callResult map[string]any
	if err := json.Unmarshal(callResp.Result, &callResult); err != nil {
		t.Fatalf("decode tools/call result: %v", err)
	}
	if callResult["isError"] == true {
		t.Fatalf("tools/call reported an error: %v", callResult)
	}

	want := map[string]any{"text": "API error rate is high"}
	if len(gotBodies) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(gotBodies))
	}
	for i, got := range gotBodies {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("upstream body %d = %#v, want %#v", i, got, want)
		}
	}
}

func TestBodyMappingRejectsMissingAndNonStringWithoutUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer upstream.Close()

	s, err := New("mapper", "mapper.mcp.kdl", []byte(bodyMappingSpec(upstream.URL)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	cases := map[string]string{
		"missing":    `{"commonAnnotations":{}}`,
		"non-string": `{"commonAnnotations":{"summary":42}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := postAPI(t, ts.Client(), ts.URL+"/api/create_message", "application/json", body)
			result := decodeAPIResult(t, resp)
			if resp.StatusCode != http.StatusBadGateway || result["isError"] != true {
				t.Fatalf("status/body = %d %v, want 502 and isError=true", resp.StatusCode, result)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("upstream calls = %d, want 0", got)
	}
}
