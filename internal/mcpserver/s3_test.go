package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeS3 struct {
	put  *s3.PutObjectInput
	body []byte
	list *s3.ListObjectsV2Input
}

func (f *fakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	f.put = in
	if in.Body != nil {
		f.body, _ = io.ReadAll(in.Body)
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.list = in
	return &s3.ListObjectsV2Output{
		Contents:    []types.Object{{Key: aws.String("charts/latency.png"), Size: aws.Int64(12)}},
		IsTruncated: aws.Bool(false),
	}, nil
}

func (f *fakeS3) HeadObject(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return &s3.HeadObjectOutput{ContentLength: aws.Int64(12), ContentType: aws.String("image/png")}, nil
}

func testS3Policy() s3Policy {
	return s3Policy{
		Region:       "us-east-1",
		Bucket:       "coilysiren-public-assets",
		BaseURL:      "https://files.coilysiren.me",
		MaxBytes:     32,
		ContentTypes: []string{"image/png", "application/json"},
	}
}

func callPut(t *testing.T, client s3Client, policy s3Policy, key, b64, ctype string) *mcp.CallToolResult {
	t.Helper()
	args, err := json.Marshal(map[string]string{"key": key, "content_base64": b64, "content_type": ctype})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s3PutHandler(client, policy)(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	})
	if err != nil {
		t.Fatalf("handler returned a transport error: %v", err)
	}
	return res
}

func TestParseS3Policy(t *testing.T) {
	src := []byte(`wrap ward mcp aws s3 {
    region "us-east-1"
    bucket "coilysiren-public-assets"
    base-url "https://files.coilysiren.me"
    prefix "dowel"
    max-bytes 1048576
    content-type "image/png"
    content-type "application/json"
    can put object
    can list objects
    can get object-url
}`)
	got, err := parseS3Policy(src)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bucket != "coilysiren-public-assets" || got.BaseURL != "https://files.coilysiren.me" {
		t.Fatalf("policy = %#v", got)
	}
	if got.MaxBytes != 1048576 {
		t.Errorf("maxBytes = %d, want 1048576", got.MaxBytes)
	}
	if got.Prefix != "dowel/" {
		t.Errorf("prefix = %q, want dowel/", got.Prefix)
	}
	if len(got.ContentTypes) != 2 {
		t.Errorf("contentTypes = %v", got.ContentTypes)
	}
}

// A delete grant is the one an operator is most likely to reach for, so the
// parser has to refuse it by name rather than ignore it.
func TestS3PolicyRefusesDeleteGrant(t *testing.T) {
	src := []byte(`wrap ward mcp aws s3 {
    bucket "b"
    base-url "https://example.test"
    content-type "image/png"
    can put object
    can list objects
    can get object-url
    can delete object
}`)
	if _, err := parseS3Policy(src); err == nil {
		t.Fatal("a delete grant should fail closed")
	}
}

// The guardfile is the only place this bound exists. IAM cannot see a
// Content-Type header, so a policy listing HTML must refuse to start.
func TestS3PolicyRefusesServableMarkup(t *testing.T) {
	for _, ct := range []string{"text/html", "image/svg+xml", "application/xhtml+xml"} {
		src := []byte(`wrap ward mcp aws s3 {
    bucket "b"
    base-url "https://example.test"
    content-type "` + ct + `"
    can put object
    can list objects
    can get object-url
}`)
		if _, err := parseS3Policy(src); err == nil {
			t.Errorf("content-type %q should fail closed", ct)
		}
	}
}

func TestS3PolicyNeedsBucketBaseURLAndContentType(t *testing.T) {
	cases := map[string]string{
		"no bucket": `wrap ward mcp aws s3 {
    base-url "https://example.test"
    content-type "image/png"
    can put object
    can list objects
    can get object-url
}`,
		"no base-url": `wrap ward mcp aws s3 {
    bucket "b"
    content-type "image/png"
    can put object
    can list objects
    can get object-url
}`,
		"no content-type": `wrap ward mcp aws s3 {
    bucket "b"
    base-url "https://example.test"
    can put object
    can list objects
    can get object-url
}`,
	}
	for name, src := range cases {
		if _, err := parseS3Policy([]byte(src)); err == nil {
			t.Errorf("%s should fail closed", name)
		}
	}
}

func TestS3KeysAreRefusedRatherThanEscaped(t *testing.T) {
	policy := testS3Policy()
	for _, key := range []string{
		"",
		"/leading",
		"trailing/",
		"a//b",
		"../escape.png",
		"dir/../escape.png",
		"has space.png",
		"semi;colon.png",
		"back\\slash.png",
		"quote\".png",
		"percent%2e%2e.png",
	} {
		if _, err := policy.resolveKey(key); err == nil {
			t.Errorf("key %q should be refused", key)
		}
	}
	got, err := policy.resolveKey("charts/latency-p99_2026.png")
	if err != nil {
		t.Fatalf("a plain key was refused: %v", err)
	}
	if got != "charts/latency-p99_2026.png" {
		t.Errorf("key = %q", got)
	}
}

func TestS3PrefixConfinesTheKey(t *testing.T) {
	policy := testS3Policy()
	policy.Prefix = "dowel/"
	got, err := policy.resolveKey("chart.png")
	if err != nil {
		t.Fatal(err)
	}
	if got != "dowel/chart.png" {
		t.Errorf("key = %q, want dowel/chart.png", got)
	}
}

func TestS3PutRejectsContentTypeOutsideThePolicy(t *testing.T) {
	client := &fakeS3{}
	body := base64.StdEncoding.EncodeToString([]byte("hello"))
	res := callPut(t, client, testS3Policy(), "note.html", body, "text/html")
	if !res.IsError {
		t.Fatal("a content type outside the policy reported success")
	}
	if client.put != nil {
		t.Error("the refused call still reached S3")
	}
}

// The cap is measured from the decoded bytes, so a caller cannot understate the
// size and have the check pass on the claim.
func TestS3PutEnforcesTheSizeCapOnDecodedBytes(t *testing.T) {
	client := &fakeS3{}
	policy := testS3Policy()
	oversized := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 64)))
	res := callPut(t, client, policy, "big.png", oversized, "image/png")
	if !res.IsError {
		t.Fatal("an oversized upload reported success")
	}
	if client.put != nil {
		t.Error("the oversized call still reached S3")
	}
}

func TestS3PutRejectsEmptyAndMalformedBodies(t *testing.T) {
	client := &fakeS3{}
	policy := testS3Policy()
	if res := callPut(t, client, policy, "empty.png", "", "image/png"); !res.IsError {
		t.Error("an empty body reported success")
	}
	if res := callPut(t, client, policy, "bad.png", "not!base64!", "image/png"); !res.IsError {
		t.Error("a malformed body reported success")
	}
	if client.put != nil {
		t.Error("a refused call still reached S3")
	}
}

func TestS3PutSendsTheContentTypeAndReturnsThePublicURL(t *testing.T) {
	client := &fakeS3{}
	policy := testS3Policy()
	res := callPut(t, client, policy, "charts/latency.png",
		base64.StdEncoding.EncodeToString([]byte("pretend-png")), "image/png")
	if res.IsError {
		t.Fatalf("put failed: %v", res.Content)
	}
	if aws.ToString(client.put.Bucket) != "coilysiren-public-assets" {
		t.Errorf("bucket = %q", aws.ToString(client.put.Bucket))
	}
	if aws.ToString(client.put.Key) != "charts/latency.png" {
		t.Errorf("key = %q", aws.ToString(client.put.Key))
	}
	// exec's demo-asset-host.sh recorded that a guessed octet-stream will not
	// render inline in Discord, so the header has to be explicit on the wire.
	if aws.ToString(client.put.ContentType) != "image/png" {
		t.Errorf("contentType = %q, want image/png", aws.ToString(client.put.ContentType))
	}
	if string(client.body) != "pretend-png" {
		t.Errorf("body = %q", client.body)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var structured map[string]any
	if err := json.Unmarshal(raw, &structured); err != nil {
		t.Fatal(err)
	}
	// The bucket is private behind CloudFront, so an S3 endpoint in the link
	// would be a URL that 403s for every reader it is handed to.
	if structured["url"] != "https://files.coilysiren.me/charts/latency.png" {
		t.Errorf("url = %v", structured["url"])
	}
	if strings.Contains(structured["url"].(string), "s3") {
		t.Errorf("url leaks an S3 endpoint: %v", structured["url"])
	}
	if structured["bytes"] != float64(len("pretend-png")) {
		t.Errorf("bytes = %v", structured["bytes"])
	}
}

func TestS3ListConfinesThePrefixAndReturnsPublicURLs(t *testing.T) {
	client := &fakeS3{}
	policy := testS3Policy()
	policy.Prefix = "dowel/"
	args, err := json.Marshal(map[string]string{"prefix": "charts"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s3ListHandler(client, policy)(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("list failed: %v", res.Content)
	}
	if aws.ToString(client.list.Prefix) != "dowel/charts" {
		t.Errorf("prefix = %q, want dowel/charts", aws.ToString(client.list.Prefix))
	}
}

func TestS3ListRefusesATraversingPrefix(t *testing.T) {
	client := &fakeS3{}
	args, err := json.Marshal(map[string]string{"prefix": "../other"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s3ListHandler(client, testS3Policy())(context.Background(), &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{Arguments: args},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("a traversing prefix reported success")
	}
	if client.list != nil {
		t.Error("the refused call still reached S3")
	}
}

// The publish tool must not claim to be read-only. A roster that trusts
// annotations would otherwise treat an upload as a safe call.
func TestS3ToolsAdvertiseTheirWriteShape(t *testing.T) {
	s, err := newS3Server("s3", "s3.mcp.kdl", testS3Policy(), &fakeS3{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.tools) != 3 {
		t.Fatalf("tool count = %d, want 3", len(s.tools))
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range s.tools {
		if tool.Title == "" {
			t.Errorf("%s title is empty", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s output schema is nil", tool.Name)
		}
		byName[tool.Name] = tool
	}
	put := byName["put_object"]
	if put == nil {
		t.Fatal("put_object is missing")
	}
	if put.Annotations.ReadOnlyHint {
		t.Error("put_object advertises readOnlyHint")
	}
	if put.Annotations.IdempotentHint {
		t.Error("put_object advertises idempotentHint, but the same key twice serves different bytes")
	}
	for _, name := range []string{"list_objects", "get_object_url"} {
		tool := byName[name]
		if tool == nil {
			t.Fatalf("%s is missing", name)
		}
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s does not advertise readOnlyHint", name)
		}
	}
}
