package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func specWithInstructions(body string) string {
	return body + `
wrap ward mcp test {
    base-url "http://127.0.0.1:1"
    auth bearer { value literal "unused" }
    can get thing {
        path "/things/{id}"
    }
}`
}

// initializeInstructions serves a spec and reads the Instructions the handshake
// actually publishes, rather than the constant the code happens to hold.
func initializeInstructions(t *testing.T, spec string) string {
	t.Helper()
	s, err := New("test", "test.mcp.kdl", []byte(spec))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"mcp-beaver-test","version":"0.1.0"}}}`)
	out := decodeRPCResponse(t, resp)
	if out.Error != nil {
		t.Fatalf("initialize error: %+v", out.Error)
	}
	var result struct {
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("initialize result: %v", err)
	}
	return result.Instructions
}

// The whole point of #77: two guardfiles must not hand a client the same
// sentence. A roster holding four beaver servers learned nothing from four
// byte-identical strings.
func TestInstructionsDifferPerGuardfile(t *testing.T) {
	forgejo := initializeInstructions(t, specWithInstructions(`instructions {
    text "Issues, pull requests and repository metadata on the Coilyco Forgejo."
    text "Reach for it to read or file tracker work."
}`))
	signoz := initializeInstructions(t, specWithInstructions(`instructions {
    text "Traces, logs and metrics from the homelab SigNoz."
}`))

	if forgejo == signoz {
		t.Fatalf("two guardfiles published identical instructions: %q", forgejo)
	}
	for name, got := range map[string]string{"forgejo": forgejo, "signoz": signoz} {
		if !strings.HasPrefix(got, serverInstructions) {
			t.Errorf("%s instructions dropped the shared policy sentence: %q", name, got)
		}
	}
	if !strings.Contains(forgejo, "read or file tracker work") {
		t.Errorf("forgejo instructions lost a text line: %q", forgejo)
	}
	if !strings.Contains(signoz, "homelab SigNoz") {
		t.Errorf("signoz instructions lost its text: %q", signoz)
	}
}

// A guardfile that declares nothing publishes exactly what it published before,
// so no deployed image changes until its spec opts in.
func TestInstructionsDefaultToTheSharedSentenceAlone(t *testing.T) {
	if got := initializeInstructions(t, specWithInstructions("")); got != serverInstructions {
		t.Fatalf("instructions = %q, want the shared sentence unchanged", got)
	}
}

func TestParseInstructionsRejectsMalformedNodes(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"argument form":  {`instructions "a one liner"`, "takes no arguments"},
		"property":       {"instructions text=\"a one liner\"", "takes no properties"},
		"unknown child":  {"instructions {\n    describe \"nope\"\n}", `unknown ` + "`instructions`" + ` child "describe"`},
		"no text":        {"instructions {\n}", "at least one non-empty"},
		"empty text":     {"instructions {\n    text \"\"\n}", "at least one non-empty"},
		"duplicate node": {"instructions {\n    text \"one\"\n}\ninstructions {\n    text \"two\"\n}", "duplicate `instructions`"},
		"over budget":    {"instructions {\n    text \"" + strings.Repeat("x", maxInstructionsLen+1) + "\"\n}", "over the 500-character budget"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseInstructions([]byte(specWithInstructions(tc.body)))
			if err == nil {
				t.Fatalf("parseInstructions accepted %q", tc.body)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to name %q", err, tc.want)
			}
		})
	}
}

// Exactly at the budget is legal - the cap is a budget, and an off-by-one that
// rejects the stated maximum is the kind of thing an author debugs for an hour.
func TestParseInstructionsAcceptsTheBudgetExactly(t *testing.T) {
	body := "instructions {\n    text \"" + strings.Repeat("x", maxInstructionsLen) + "\"\n}"
	got, err := parseInstructions([]byte(specWithInstructions(body)))
	if err != nil {
		t.Fatalf("parseInstructions: %v", err)
	}
	if len(got) != maxInstructionsLen {
		t.Fatalf("len = %d, want %d", len(got), maxInstructionsLen)
	}
}
