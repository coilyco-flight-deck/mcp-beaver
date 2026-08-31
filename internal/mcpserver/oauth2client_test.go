package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// tokenEndpoint is a real client_credentials token endpoint, counting mints so
// a test can prove the cache rather than assume it.
func tokenEndpoint(t *testing.T, mints *int64) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			http.Error(w, "wrong grant", http.StatusBadRequest)
			return
		}
		id, secret, ok := r.BasicAuth()
		if !ok || id != "beaver" || secret != "s3cret-client" {
			http.Error(w, "bad client", http.StatusUnauthorized)
			return
		}
		atomic.AddInt64(mints, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"minted-token-value","token_type":"Bearer","expires_in":3600}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func oauth2SpecServer(t *testing.T, tokenURL, upstream string) *Server {
	t.Helper()
	t.Setenv("MCP_BEAVER_TEST_CLIENT_SECRET", "s3cret-client")
	dir := t.TempDir()
	spec := `oauth2-client "up" {
    token-url "` + tokenURL + `"
    client-id "beaver"
    client-secret env "MCP_BEAVER_TEST_CLIENT_SECRET"
}

wrap ward mcp things {
    base-url "` + upstream + `"
    auth bearer { value oauth2 "up" }
    can get thing { path "/things/{id}" }
}`
	path := filepath.Join(dir, "test.mcp.kdl")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	src, err := os.ReadFile(path) //nolint:gosec // test fixture written above
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	s, err := New("test", path, src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// The whole point, end to end: a guardfile declares a client, a grant presents
// `value oauth2`, and the upstream receives a token that was minted rather than
// read from anywhere. Asserting the parse alone would pass with the provider
// never registered.
func TestOAuth2MintedTokenReachesTheUpstream(t *testing.T) {
	var mints int64
	token := tokenEndpoint(t, &mints)

	var presented string
	var calls int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = r.Header.Get("Authorization")
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"id":"1"}`)
	}))
	defer upstream.Close()

	s := oauth2SpecServer(t, token.URL, upstream.URL)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	for i := 0; i < 3; i++ {
		resp := postToServer(t, ts.Client(), ts.URL+"/mcp", "",
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_thing","arguments":{"id":"1"}}}`)
		decodeRPCResponse(t, resp)
	}

	if presented != "Bearer minted-token-value" {
		t.Errorf("upstream saw Authorization %q, want the minted token", presented)
	}
	if got := atomic.LoadInt64(&calls); got != 3 {
		t.Fatalf("upstream calls = %d, want 3", got)
	}
	// One mint for three calls. Minting per call would work and would hammer
	// the token endpoint, which is the failure tokenmint's cache exists to
	// prevent, and it is invisible from the response.
	if got := atomic.LoadInt64(&mints); got != 1 {
		t.Errorf("token endpoint minted %d times for 3 calls, want 1 cached mint", got)
	}
}

// A minted address is a declared client name, so a typo is a build error rather
// than a 401 on the first call in production.
func TestOAuth2UndeclaredClientIsRefusedOffline(t *testing.T) {
	dir := t.TempDir()
	spec := `oauth2-client "up" {
    token-url "https://auth.example.com/token"
    client-id "beaver"
    client-secret env "MCP_BEAVER_TEST_CLIENT_SECRET"
}

pin "get_thing" {
    query "token" oauth2 "typo"
}

wrap ward mcp things {
    base-url "http://127.0.0.1:1"
    auth bearer { value oauth2 "up" }
    can get thing { path "/things/{id}" }
}`
	path := filepath.Join(dir, "test.mcp.kdl")
	if err := os.WriteFile(path, []byte(spec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	src, _ := os.ReadFile(path) //nolint:gosec // test fixture written above
	_, err := New("test", path, src)
	if err == nil || !strings.Contains(err.Error(), `no `+"`oauth2-client`"+` declares`) {
		t.Errorf("err = %v, want the undeclared client named", err)
	}
}

// The registry seam itself. Before #83 six sites called valuesource.Builtins()
// for themselves and two were validators, so a declared provider resolved at
// runtime and was refused at validation. This is that shape directly.
func TestValidatorSeesAConsumerRegisteredProvider(t *testing.T) {
	var mints int64
	token := tokenEndpoint(t, &mints)
	declared := oauth2SpecServer(t, token.URL, "http://127.0.0.1:1").providers
	if err := declared.checkSource(oauth2Provider, "up"); err != nil {
		t.Errorf("checkSource on a declared client = %v, want nil", err)
	}
	if err := BaseProviders().checkSource(oauth2Provider, "up"); err == nil {
		t.Error("the base registry accepted oauth2, so this test cannot detect the seam")
	}
	// Two distinct failures stay distinct: an unknown provider is a name this
	// server never had, and an undeclared address is a real provider pointed at
	// a client nobody declared.
	unknown := declared.checkSource("vault", "anything")
	if unknown == nil || !strings.Contains(unknown.Error(), "unknown provider") {
		t.Errorf("unknown provider error = %v", unknown)
	}
	undeclared := declared.checkSource(oauth2Provider, "typo")
	if undeclared == nil || !strings.Contains(undeclared.Error(), "no `oauth2-client` declares") {
		t.Errorf("undeclared client error = %v", undeclared)
	}
}

func TestOAuth2ClientFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		node string
		want string
	}{
		{"no token-url", `oauth2-client "a" { client-id "x"; client-secret env "S" }`, "needs a `token-url`"},
		{"http token-url", `oauth2-client "a" { token-url "http://auth.example.com/t"; client-id "x"; client-secret env "S" }`, "must be https"},
		{"no client-id", `oauth2-client "a" { token-url "https://a/t"; client-secret env "S" }`, "needs a `client-id`"},
		{"no secret", `oauth2-client "a" { token-url "https://a/t"; client-id "x" }`, "needs a `client-secret"},
		{"minted secret", `oauth2-client "a" { token-url "https://a/t"; client-id "x"; client-secret oauth2 "a" }`, "a minted value cannot seed a mint"},
		{"unknown child", `oauth2-client "a" { token-url "https://a/t"; client-id "x"; client-secret env "S"; audience "y" }`, "unknown `oauth2-client` child"},
		{"bad auth-style", `oauth2-client "a" { token-url "https://a/t"; client-id "x"; client-secret env "S"; auth-style "digest" }`, "is not basic or post"},
		{"duplicate", `oauth2-client "a" { token-url "https://a/t"; client-id "x"; client-secret env "S" }
oauth2-client "a" { token-url "https://b/t"; client-id "y"; client-secret env "S" }`, "duplicate `oauth2-client`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseOAuth2Clients(singleSource([]byte(tc.node+"\n\n"+roundTripSpec("http://127.0.0.1:1")), "."))
			if err == nil {
				t.Fatalf("parse succeeded, want a refusal naming %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// The CLI form is what upstream mode has instead of a guardfile, so it refuses
// the same shapes. A literal secret is refused outright: unlike a header there
// is no reading of a constant client secret in argv that is not a mistake.
func TestParseOAuth2ClientFlag(t *testing.T) {
	client, err := ParseOAuth2Client(`name=moxn,token-url=https://x.example.dev/token,client-id=beaver,client-secret={env:MOXN_SECRET},scope=profile email`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if client.Name != "moxn" || client.ClientID != "beaver" {
		t.Errorf("client = %+v", client)
	}
	if client.ClientSecret.Provider != "env" || client.ClientSecret.Address != "MOXN_SECRET" {
		t.Errorf("secret source = %+v, want the env span", client.ClientSecret)
	}
	if len(client.Scopes) != 2 || client.Scopes[0] != "profile" || client.Scopes[1] != "email" {
		t.Errorf("scopes = %v, want both", client.Scopes)
	}

	for _, tc := range []struct{ name, raw, want string }{
		{"bare secret", `name=a,token-url=https://a/t,client-id=x,client-secret=hunter2`, "must be a {provider:address} span"},
		{"no name", `token-url=https://a/t,client-id=x,client-secret={env:S}`, "needs a `name=`"},
		{"unknown field", `name=a,token-url=https://a/t,client-id=x,client-secret={env:S},audience=y`, "unknown field"},
		{"not a pair", `name=a,token-url`, "must be <key>=<value>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseOAuth2Client(tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to name %q", err, tc.want)
			}
		})
	}
}

// The credential must not reach the surface an operator reads.
func TestAdminReportsClientNamesAndNoCredential(t *testing.T) {
	var mints int64
	token := tokenEndpoint(t, &mints)
	s := oauth2SpecServer(t, token.URL, "http://127.0.0.1:1")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + adminDescribePath)
	if err != nil {
		t.Fatalf("admin: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if !strings.Contains(string(raw), `"oauth2Clients":["up"]`) {
		t.Errorf("admin config = %s, want the client name reported", raw)
	}
	for _, secret := range []string{"s3cret-client", "minted-token-value", token.URL} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("admin body carries %q, which must never leave the process", secret)
		}
	}
}
