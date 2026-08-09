package mcpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// specWithSiblings exercises `resource` and `prompt` riding beside `wrap`.
func specWithSiblings() string {
	return `resource "oncall-runbook" uri="ward://runbook/oncall" mime="text/markdown" {
    description "First response for an upstream 5xx"
    text "1. Check /healthz on the pod."
    text "2. Check the upstream status page."
}

prompt "triage" title="Triage an incident" {
    description "Walk the on-call first-response steps"
    argument "service" description="Which service is failing" required=#true
    argument "since" description="How far back to look"
    text "You are triaging {service}."
    text "Look back {since}."
}

` + roundTripSpec("http://127.0.0.1:1")
}

func siblingServer(t *testing.T) *httptest.Server {
	t.Helper()
	s, err := New("test", "test.mcp.kdl", []byte(specWithSiblings()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return httptest.NewServer(s.Handler())
}

func TestResourcesListAndRead(t *testing.T) {
	ts := siblingServer(t)
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`)
	var list map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &list); err != nil {
		t.Fatalf("list result: %v", err)
	}
	items, _ := list["resources"].([]any)
	if len(items) != 1 {
		t.Fatalf("resources = %v, want exactly the one declared", list["resources"])
	}
	first, _ := items[0].(map[string]any)
	if first["uri"] != "ward://runbook/oncall" {
		t.Errorf("uri = %v", first["uri"])
	}
	if first["mimeType"] != "text/markdown" {
		t.Errorf("mimeType = %v, want the declared text/markdown", first["mimeType"])
	}

	read := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ward://runbook/oncall"}}`)
	var body map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, read).Result, &body); err != nil {
		t.Fatalf("read result: %v", err)
	}
	contents, _ := body["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %v", body["contents"])
	}
	text, _ := contents[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "1. Check /healthz") || !strings.Contains(text, "2. Check the upstream") {
		t.Errorf("text = %q, want both declared lines joined", text)
	}
}

func TestPromptsListAndGet(t *testing.T) {
	ts := siblingServer(t)
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`)
	var list map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &list); err != nil {
		t.Fatalf("list result: %v", err)
	}
	items, _ := list["prompts"].([]any)
	if len(items) != 1 {
		t.Fatalf("prompts = %v", list["prompts"])
	}
	args, _ := items[0].(map[string]any)["arguments"].([]any)
	if len(args) != 2 {
		t.Fatalf("arguments = %v, want the two declared", args)
	}

	get := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"triage","arguments":{"service":"signoz","since":"1h"}}}`)
	var body map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, get).Result, &body); err != nil {
		t.Fatalf("get result: %v", err)
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	}
	content, _ := messages[0].(map[string]any)["content"].(map[string]any)
	text, _ := content["text"].(string)
	if !strings.Contains(text, "triaging signoz") || !strings.Contains(text, "back 1h") {
		t.Errorf("text = %q, want both arguments substituted", text)
	}
}

// A half-filled prompt reads as a complete one to the model, so a missing
// required argument fails rather than substituting empty.
func TestPromptsGetRejectsMissingRequiredArgument(t *testing.T) {
	ts := siblingServer(t)
	defer ts.Close()

	get := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
		`{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":{"name":"triage","arguments":{"since":"1h"}}}`)
	out := decodeRPCResponse(t, get)
	if out.Error == nil {
		t.Fatalf("prompts/get accepted a missing required argument: %s", out.Result)
	}
	if !strings.Contains(out.Error.Message, "service") {
		t.Errorf("error = %q, want it to name the missing argument", out.Error.Message)
	}
}

// Deny-by-absence extends to the new node types: a spec that declares neither
// must advertise neither capability, exactly as an unwritten grant mints no
// tool. This is the property that keeps existing guardfiles unchanged.
func TestSpecWithoutSiblingsAdvertisesNoResourcesOrPrompts(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	var result map[string]any
	if err := json.Unmarshal(decodeRPCResponse(t, resp).Result, &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["resources"]; ok {
		t.Errorf("capabilities advertise resources for a spec with none: %v", caps)
	}
	if _, ok := caps["prompts"]; ok {
		t.Errorf("capabilities advertise prompts for a spec with none: %v", caps)
	}
}

func TestSiblingNodesFailClosed(t *testing.T) {
	for name, spec := range map[string]string{
		"resource without uri":  `resource "r" { text "x" }`,
		"resource without text": `resource "r" uri="ward://r"`,
		"resource unknown prop": `resource "r" uri="ward://r" colour="red" { text "x" }`,
		"duplicate uri":         `resource "a" uri="ward://r" { text "x" }` + "\n" + `resource "b" uri="ward://r" { text "y" }`,
		"prompt without text":   `prompt "p" { description "d" }`,
		"prompt unknown child":  `prompt "p" { banana "x" }`,
		"prompt unknown prop":   `prompt "p" colour="red" { text "x" }`,
		"duplicate prompt":      `prompt "p" { text "x" }` + "\n" + `prompt "p" { text "y" }`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("test", "test.mcp.kdl", []byte(spec+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
			if err == nil {
				t.Fatal("New accepted a malformed sibling node")
			}
		})
	}
}

// Value.String() renders a KDL bool as "<kdl.Bool true>", so a string compare
// silently reads every boolean as false and quietly downgrades a required
// argument to optional. Pin the parse rather than only the behaviour above.
func TestPromptArgumentRequiredParsesAsBool(t *testing.T) {
	prompts, err := parsePrompts([]byte(`prompt "p" {
    argument "yes" required=#true
    argument "no" required=#false
    argument "unset"
    text "x"
}`))
	if err != nil {
		t.Fatalf("parsePrompts: %v", err)
	}
	got := map[string]bool{}
	for _, arg := range prompts[0].prompt.Arguments {
		got[arg.Name] = arg.Required
	}
	if !got["yes"] {
		t.Error(`required=#true parsed as optional`)
	}
	if got["no"] || got["unset"] {
		t.Errorf("required defaulted true: %v", got)
	}
}

func TestPromptArgumentRequiredRejectsNonBool(t *testing.T) {
	_, err := parsePrompts([]byte(`prompt "p" { argument "a" required="yes"` + "\n" + `text "x" }`))
	if err == nil {
		t.Fatal("parsePrompts accepted a non-boolean required")
	}
	if !strings.Contains(err.Error(), "boolean") {
		t.Errorf("error = %q, want it to name the expected type", err)
	}
}
