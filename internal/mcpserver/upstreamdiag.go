package mcpserver

import (
	"bytes"
	"io"
	"net/http"
)

// maxDiagnosedBody bounds what a rejection is allowed to log. The upstream's
// reason is a sentence; anything longer is a page and not worth a log line.
const maxDiagnosedBody = 512

// withUpstreamDiagnostics logs the upstream's own reason for a 4xx or 5xx.
//
// The SDK surfaces only the status text, so a rejection reaches mcp-beaver as
// the bare string "Bad Request" and the server's explanation is discarded
// unread. That one omission has now misdirected three investigations of
// mcp-beaver#85, so the body is read here and put in the log beside it.
func withUpstreamDiagnostics(client *http.Client) *http.Client {
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	out := *client
	out.Transport = &upstreamDiagTransport{base: base}
	return &out
}

type upstreamDiagTransport struct {
	base http.RoundTripper
}

func (t *upstreamDiagTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode < 400 {
		return resp, err
	}
	// The body is replaced rather than consumed, so the caller still reads a
	// complete response and this stays a pure observer.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxDiagnosedBody))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil {
		return resp, nil
	}
	Log().Warn("upstream rejected a request",
		"status", resp.Status,
		"method", req.Method,
		"url", req.URL.String(),
		"session", req.Header.Get("Mcp-Session-Id"),
		"protocol_version", req.Header.Get("MCP-Protocol-Version"),
		"upstream_reason", string(bytes.TrimSpace(body)),
	)
	return resp, nil
}
