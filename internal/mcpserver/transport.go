package mcpserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Handler mounts the server on an http.ServeMux over both MCP HTTP transports:
//
//   - streamable HTTP (2025-03-26+) at /mcp - one POST carries a JSON-RPC
//     message, the reply comes back as JSON or an SSE frame per the client's
//     Accept header. This is the modern default.
//   - legacy HTTP+SSE (2024-11-05) at /sse + /messages - GET /sse opens the
//     event stream and names the POST-back endpoint, POST /messages feeds a
//     JSON-RPC message whose reply is pushed over that stream.
//
// Both never bind stdio: these images run as remote pods reached by URL. A
// health probe answers at /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sse := &sseSessions{sessions: map[string]chan []byte{}}
	mux.HandleFunc("/mcp", s.handleStreamable)
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) { s.handleSSEOpen(w, r, sse) })
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) { s.handleSSEMessage(w, r, sse) })
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}

// handleStreamable serves the streamable-HTTP transport at /mcp. A POST carries
// one JSON-RPC message; its reply is returned inline as JSON, or as a single SSE
// frame when the client accepts text/event-stream. A notification returns 202.
func (s *Server) handleStreamable(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		// ok, handled below
	case http.MethodDelete:
		// Stateless: nothing to tear down for a session close.
		w.WriteHeader(http.StatusOK)
		return
	default:
		// We hold no server-initiated stream to offer on a bare GET.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		writeJSON(w, http.StatusOK, errorResponse(nil, codeParseError, "read body: "+err.Error()))
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusOK, errorResponse(nil, codeParseError, "parse JSON-RPC: "+err.Error()))
		return
	}
	resp := s.dispatch(r.Context(), req)
	if resp == nil {
		// A notification takes no reply.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if acceptsEventStream(r) {
		writeSSEFrame(w, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// sseSessions tracks the open legacy-SSE streams by session id so a POST to
// /messages can push its reply onto the right stream.
type sseSessions struct {
	mu       sync.Mutex
	sessions map[string]chan []byte
}

func (t *sseSessions) add(id string, ch chan []byte) {
	t.mu.Lock()
	t.sessions[id] = ch
	t.mu.Unlock()
}

func (t *sseSessions) get(id string) (chan []byte, bool) {
	t.mu.Lock()
	ch, ok := t.sessions[id]
	t.mu.Unlock()
	return ch, ok
}

func (t *sseSessions) remove(id string) {
	t.mu.Lock()
	delete(t.sessions, id)
	t.mu.Unlock()
}

// handleSSEOpen serves GET /sse: it opens the event stream, announces the
// per-session POST-back endpoint, then relays every reply pushed onto the
// session channel until the client disconnects.
func (s *Server) handleSSEOpen(w http.ResponseWriter, r *http.Request, t *sseSessions) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	id := newSessionID()
	ch := make(chan []byte, 16)
	t.add(id, ch)
	defer t.remove(id)

	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	// The MCP legacy transport's first event names where the client POSTs.
	writeRawSSE(w, "endpoint", "/messages?sessionId="+id)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			writeRawSSE(w, "message", string(msg))
			flusher.Flush()
		}
	}
}

// handleSSEMessage serves POST /messages?sessionId=...: it dispatches the
// JSON-RPC message and pushes any reply onto the named session's stream,
// answering the POST itself with 202 Accepted (the reply travels over /sse).
func (s *Server) handleSSEMessage(w http.ResponseWriter, r *http.Request, t *sseSessions) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("sessionId")
	ch, ok := t.get(id)
	if !ok {
		http.Error(w, "unknown or closed session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "parse JSON-RPC: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp := s.dispatch(r.Context(), req)
	if resp != nil {
		if raw, err := json.Marshal(resp); err == nil {
			select {
			case ch <- raw:
			default: // a wedged client must not block the request goroutine
			}
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// maxRequestBytes caps an inbound message so a runaway body cannot exhaust the
// pod. MCP tool arguments are small; 8 MiB is generous headroom.
const maxRequestBytes = 8 << 20

// acceptsEventStream reports whether the client's Accept header opts into an SSE
// reply on the streamable-HTTP endpoint.
func acceptsEventStream(r *http.Request) bool {
	for _, a := range r.Header.Values("Accept") {
		for _, part := range strings.Split(a, ",") {
			media := strings.TrimSpace(part)
			if i := strings.IndexByte(media, ';'); i >= 0 {
				media = strings.TrimSpace(media[:i])
			}
			if media == "text/event-stream" {
				return true
			}
		}
	}
	return false
}

// writeJSON writes v as a JSON-RPC HTTP response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeSSEFrame writes a single JSON-RPC response as one SSE `message` event on
// the streamable-HTTP endpoint, then lets the handler return (stream closes).
func writeSSEFrame(w http.ResponseWriter, v any) {
	setSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	writeRawSSE(w, "message", string(raw))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// setSSEHeaders sets the headers common to both SSE endpoints.
func setSSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
}

// writeRawSSE writes one `event:`/`data:` SSE frame.
func writeRawSSE(w io.Writer, event, data string) {
	_, _ = io.WriteString(w, "event: "+event+"\ndata: "+data+"\n\n")
}

// newSessionID mints an unguessable legacy-SSE session id.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
