package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/calico32/kdl-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// THE FIRST WRITE-CAPABLE SDK MODE IN THIS RUNTIME.
//
// `serve-ssm` reads one parameter and can do nothing else, so its whole
// boundary is one ARN. A publisher is different in kind: the caller supplies
// the bytes, the key, and the content type, and whatever lands is served to
// the public internet under Kai's domain. IAM cannot express most of what
// matters there, because to IAM every PutObject inside the bucket looks alike.
//
// So the guards below are not decoration on top of an IAM grant. They are the
// only place several of these bounds exist at all:
//
//   - CONTENT TYPE IS AN EXACT ALLOWLIST, and text/html is not in the shipped
//     one. An HTML file served from the same origin as everything else Kai
//     publishes is a stored-XSS and phishing surface wearing her domain name.
//     IAM has no opinion about the Content-Type header, so nothing else can
//     refuse this.
//   - KEYS ARE A NARROW CHARACTER SET with no traversal segments. A key is a
//     public URL path, and a key holding a control character, a backslash, or
//     a `..` segment produces a link that behaves differently in the CDN, the
//     bucket, and the browser.
//   - SIZE IS CAPPED before the upload starts, from the decoded length rather
//     than the caller's claim about it.
//   - THERE IS NO DELETE TOOL AND NO OVERWRITE-BLIND PUT. Publishing is the
//     grant. Unpublishing is not, which is also why the workload user's IAM
//     policy withholds s3:DeleteObject rather than relying on this absence.
//
// The bucket is private. Reads reach the public through CloudFront, so the URL
// this hands back is the distribution's, never an S3 endpoint.

type s3Client interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	ListObjectsV2(context.Context, *s3.ListObjectsV2Input, ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

type s3Policy struct {
	Region       string
	Bucket       string
	BaseURL      string
	MaxBytes     int64
	ContentTypes []string
	Prefix       string
}

// defaultS3MaxBytes bounds one upload when the policy does not. Eight MiB is
// under Discord's own embed ceiling, so an asset that passes here is one a
// message can actually show.
const defaultS3MaxBytes int64 = 8 << 20

var s3PutOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"key":{"type":"string"},
		"url":{"type":"string"},
		"bytes":{"type":"integer"},
		"content_type":{"type":"string"}
	},
	"required":["key","url","bytes","content_type"],
	"additionalProperties":false
}`)

var s3ListOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"objects":{
			"type":"array",
			"items":{
				"type":"object",
				"properties":{
					"key":{"type":"string"},
					"url":{"type":"string"},
					"bytes":{"type":"integer"}
				},
				"required":["key","url","bytes"],
				"additionalProperties":false
			}
		},
		"truncated":{"type":"boolean"}
	},
	"required":["objects","truncated"],
	"additionalProperties":false
}`)

var s3URLOutputSchema = json.RawMessage(`{
	"type":"object",
	"properties":{
		"key":{"type":"string"},
		"url":{"type":"string"},
		"bytes":{"type":"integer"},
		"content_type":{"type":"string"}
	},
	"required":["key","url","bytes","content_type"],
	"additionalProperties":false
}`)

// NewS3 builds a publisher whose KDL policy fixes the one bucket it may write,
// the content types it accepts, and the public base URL it hands back.
func NewS3(ctx context.Context, name, specPath string, src []byte) (*Server, error) {
	policy, err := parseS3Policy(src)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(policy.Region))
	if err != nil {
		return nil, fmt.Errorf("mcp-beaver: load AWS config: %w", err)
	}
	return newS3Server(name, specPath, policy, s3.NewFromConfig(cfg))
}

func newS3Server(name, specPath string, policy s3Policy, client s3Client) (*Server, error) {
	tools := []*mcp.Tool{
		{
			Name:  "put_object",
			Title: "Publish an asset",
			Description: "Use this when the user wants to publish a file so anyone can download it. " +
				"Supply the bytes base64-encoded in content_base64, plus the content_type to serve it as. " +
				"Returns the public URL. This overwrites an existing key and cannot delete anything.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"key":{"type":"string","description":"Path under the bucket, for example charts/latency.svg"},
					"content_base64":{"type":"string","description":"The file bytes, base64-encoded"},
					"content_type":{"type":"string","description":"Exact media type to serve, and it must be one this policy allows"}
				},
				"required":["key","content_base64","content_type"],
				"additionalProperties":false
			}`),
			OutputSchema: s3PutOutputSchema,
			Annotations:  s3PublishAnnotations(),
		},
		{
			Name:         "list_objects",
			Title:        "List published assets",
			Description:  "Use this when the user wants to see what has already been published, optionally under a key prefix.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"prefix":{"type":"string"}},"additionalProperties":false}`),
			OutputSchema: s3ListOutputSchema,
			Annotations:  readOnlyOpenWorldAnnotations(),
		},
		{
			Name:         "get_object_url",
			Title:        "Get an asset's public URL",
			Description:  "Use this when the user wants the download link for an already-published key. Confirms the object exists before returning its URL.",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
			OutputSchema: s3URLOutputSchema,
			Annotations:  readOnlyOpenWorldAnnotations(),
		},
	}
	instrumentation, err := newInstrumentation("s3", tools)
	if err != nil {
		return nil, fmt.Errorf("initialize telemetry: %w", err)
	}
	s := &Server{
		name:           name,
		specPath:       specPath,
		tools:          tools,
		handlers:       make(map[string]mcp.ToolHandler, len(tools)),
		upstreams:      []adminUpstreamResponse{{Kind: "aws-s3", Mode: "sdk"}},
		sdk:            newSDKServer(name, nil, ""),
		telemetry:      instrumentation,
		requestTimeout: DefaultRequestTimeout,
	}
	s.registerTool(tools[0], s3PutHandler(client, policy))
	s.registerTool(tools[1], s3ListHandler(client, policy))
	s.registerTool(tools[2], s3URLHandler(client, policy))
	s.installMiddleware()
	return s, nil
}

// s3PublishAnnotations describes a put honestly. It is not read-only and it is
// not idempotent, because the same key twice serves different bytes the second
// time. destructive stays false because nothing is removed, and the bucket
// keeps versions so the overwrite this does allow stays recoverable.
func s3PublishAnnotations() *mcp.ToolAnnotations {
	destructive := false
	openWorld := true
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    false,
		DestructiveHint: &destructive,
		IdempotentHint:  false,
		OpenWorldHint:   &openWorld,
	}
}

func s3PutHandler(client s3Client, policy s3Policy) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Key         string `json:"key"`
			ContentB64  string `json:"content_base64"`
			ContentType string `json:"content_type"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
		}
		key, err := policy.resolveKey(args.Key)
		if err != nil {
			return toolError(err), nil
		}
		if err := policy.checkContentType(args.ContentType); err != nil {
			return toolError(err), nil
		}
		// Strict encoding, so a caller cannot smuggle bytes past the size cap
		// in the slack that a lenient decoder would forgive.
		body, err := base64.StdEncoding.DecodeString(strings.TrimSpace(args.ContentB64))
		if err != nil {
			return toolError(fmt.Errorf("mcp-beaver: content_base64 is not valid base64: %w", err)), nil
		}
		if len(body) == 0 {
			return toolError(fmt.Errorf("mcp-beaver: refusing to publish an empty object")), nil
		}
		// Measured from the decoded bytes, never from a length the caller states.
		if int64(len(body)) > policy.MaxBytes {
			return toolError(fmt.Errorf("mcp-beaver: %d bytes exceeds the %d byte cap this policy sets",
				len(body), policy.MaxBytes)), nil
		}
		if _, err := client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(policy.Bucket),
			Key:         aws.String(key),
			Body:        bytes.NewReader(body),
			ContentType: aws.String(args.ContentType),
		}); err != nil {
			return toolError(fmt.Errorf("put S3 object: %w", err)), nil
		}
		payload := struct {
			Key         string `json:"key"`
			URL         string `json:"url"`
			Bytes       int64  `json:"bytes"`
			ContentType string `json:"content_type"`
		}{
			Key:         key,
			URL:         policy.publicURL(key),
			Bytes:       int64(len(body)),
			ContentType: args.ContentType,
		}
		return s3Success(payload), nil
	}
}

func s3ListHandler(client s3Client, policy s3Policy) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Prefix string `json:"prefix"`
		}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
			}
		}
		// The caller's prefix narrows the policy prefix and never escapes it.
		prefix := policy.Prefix + strings.TrimPrefix(args.Prefix, "/")
		if strings.Contains(prefix, "..") {
			return toolError(fmt.Errorf("mcp-beaver: prefix %q is outside this policy", args.Prefix)), nil
		}
		out, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:  aws.String(policy.Bucket),
			Prefix:  aws.String(prefix),
			MaxKeys: aws.Int32(200),
		})
		if err != nil {
			return toolError(fmt.Errorf("list S3 objects: %w", err)), nil
		}
		type entry struct {
			Key   string `json:"key"`
			URL   string `json:"url"`
			Bytes int64  `json:"bytes"`
		}
		objects := make([]entry, 0, len(out.Contents))
		for _, obj := range out.Contents {
			key := aws.ToString(obj.Key)
			objects = append(objects, entry{
				Key:   key,
				URL:   policy.publicURL(key),
				Bytes: aws.ToInt64(obj.Size),
			})
		}
		payload := struct {
			Objects   []entry `json:"objects"`
			Truncated bool    `json:"truncated"`
		}{
			Objects:   objects,
			Truncated: aws.ToBool(out.IsTruncated),
		}
		return s3Success(payload), nil
	}
}

func s3URLHandler(client s3Client, policy s3Policy) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return toolError(fmt.Errorf("invalid tool arguments: %w", err)), nil
		}
		key, err := policy.resolveKey(args.Key)
		if err != nil {
			return toolError(err), nil
		}
		// HeadObject rather than string-building alone. A URL for a key that is
		// not there is a link someone posts and a reader gets a 404 from, and
		// the caller has no other way to tell the two apart.
		head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(policy.Bucket),
			Key:    aws.String(key),
		})
		if err != nil {
			return toolError(fmt.Errorf("head S3 object: %w", err)), nil
		}
		payload := struct {
			Key         string `json:"key"`
			URL         string `json:"url"`
			Bytes       int64  `json:"bytes"`
			ContentType string `json:"content_type"`
		}{
			Key:         key,
			URL:         policy.publicURL(key),
			Bytes:       aws.ToInt64(head.ContentLength),
			ContentType: aws.ToString(head.ContentType),
		}
		return s3Success(payload), nil
	}
}

func s3Success(payload any) *mcp.CallToolResult {
	raw, _ := json.Marshal(payload)
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: payload,
	}
}

func (p s3Policy) publicURL(key string) string {
	return strings.TrimSuffix(p.BaseURL, "/") + "/" + key
}

func (p s3Policy) checkContentType(ct string) error {
	for _, allowed := range p.ContentTypes {
		if ct == allowed {
			return nil
		}
	}
	return fmt.Errorf("mcp-beaver: content type %q is outside this policy (allowed: %s)",
		ct, strings.Join(p.ContentTypes, ", "))
}

// resolveKey turns a caller's key into the exact object key, or refuses.
//
// A key here is a public URL path, so the character set is deliberately
// narrower than what S3 itself accepts. Everything outside letters, digits,
// dot, dash, underscore, and slash is refused rather than escaped, because an
// escaped key round-trips differently through the CDN, the bucket listing, and
// a chat client that linkifies it.
func (p s3Policy) resolveKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", fmt.Errorf("mcp-beaver: key is required")
	}
	if strings.HasPrefix(key, "/") {
		return "", fmt.Errorf("mcp-beaver: key %q must be relative, with no leading slash", raw)
	}
	if strings.HasSuffix(key, "/") {
		return "", fmt.Errorf("mcp-beaver: key %q must name an object, not a folder", raw)
	}
	if strings.Contains(key, "//") {
		return "", fmt.Errorf("mcp-beaver: key %q has an empty path segment", raw)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("mcp-beaver: key %q may not traverse", raw)
		}
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '/':
		default:
			return "", fmt.Errorf("mcp-beaver: key %q may only use letters, digits, dash, underscore, dot, and slash", raw)
		}
	}
	full := p.Prefix + key
	// S3's own ceiling. Stated here so the refusal names the reason rather than
	// arriving as an opaque API error.
	if len(full) > 1024 {
		return "", fmt.Errorf("mcp-beaver: key is %d bytes, over S3's 1024 byte limit", len(full))
	}
	return full, nil
}

func parseS3Policy(src []byte) (s3Policy, error) {
	doc, err := kdl.ParseString(string(src))
	if err != nil {
		return s3Policy{}, fmt.Errorf("mcp-beaver: parse S3 KDL: %w", err)
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return s3Policy{}, fmt.Errorf("mcp-beaver: missing top-level `wrap` node")
	}
	args := wrap.Arguments()
	if len(args) != 4 || args[0].String() != "ward" || args[1].String() != "mcp" || args[2].String() != "aws" || args[3].String() != "s3" {
		return s3Policy{}, fmt.Errorf("mcp-beaver: S3 spec must start `wrap ward mcp aws s3`")
	}
	policy := s3Policy{Region: "us-east-1", MaxBytes: defaultS3MaxBytes}
	seenGrants := map[string]bool{}
	for _, node := range wrap.Children().Nodes {
		nodeArgs := node.Arguments()
		switch node.Name() {
		case "region":
			if len(nodeArgs) != 1 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: `region` needs one value")
			}
			policy.Region = nodeArgs[0].String()
		case "bucket":
			if len(nodeArgs) != 1 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: `bucket` needs one name")
			}
			policy.Bucket = nodeArgs[0].String()
		case "base-url":
			if len(nodeArgs) != 1 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: `base-url` needs one URL")
			}
			policy.BaseURL = nodeArgs[0].String()
		case "prefix":
			if len(nodeArgs) != 1 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: `prefix` needs one path")
			}
			prefix := strings.Trim(nodeArgs[0].String(), "/")
			if prefix != "" {
				policy.Prefix = prefix + "/"
			}
		case "max-bytes":
			if len(nodeArgs) != 1 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: `max-bytes` needs one integer")
			}
			// Value.String() renders a debug form for every non-string kind and
			// Value.Int() panics on one, so the kind has to be checked rather
			// than the text parsed. A quoted value is accepted too, since a
			// guardfile author writing "1048576" means the same thing.
			parsed, err := s3IntArgument(nodeArgs[0])
			if err != nil {
				return s3Policy{}, err
			}
			policy.MaxBytes = parsed
		case "content-type":
			if len(nodeArgs) != 1 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: `content-type` needs one media type")
			}
			ct := nodeArgs[0].String()
			// Refused in the parser, not the handler. A guardfile that lists it
			// should fail to start rather than start and quietly never allow it,
			// because the first shape reads as a deployment error and the second
			// reads as a working grant.
			if err := rejectServableMarkup(ct); err != nil {
				return s3Policy{}, err
			}
			policy.ContentTypes = append(policy.ContentTypes, ct)
		case "can":
			if len(nodeArgs) != 2 {
				return s3Policy{}, fmt.Errorf("mcp-beaver: S3 `can` needs verb and resource")
			}
			key := nodeArgs[0].String() + " " + nodeArgs[1].String()
			if key != "put object" && key != "list objects" && key != "get object-url" {
				return s3Policy{}, fmt.Errorf("mcp-beaver: unsupported S3 grant %q", key)
			}
			seenGrants[key] = true
		default:
			return s3Policy{}, fmt.Errorf("mcp-beaver: unknown S3 policy node %q", node.Name())
		}
	}
	if policy.Bucket == "" {
		return s3Policy{}, fmt.Errorf("mcp-beaver: S3 policy needs a `bucket`")
	}
	if policy.BaseURL == "" {
		return s3Policy{}, fmt.Errorf("mcp-beaver: S3 policy needs a `base-url` for the public reader")
	}
	if len(policy.ContentTypes) == 0 {
		return s3Policy{}, fmt.Errorf("mcp-beaver: S3 policy needs at least one `content-type`")
	}
	if !seenGrants["put object"] || !seenGrants["list objects"] || !seenGrants["get object-url"] || len(seenGrants) != 3 {
		return s3Policy{}, fmt.Errorf("mcp-beaver: S3 policy must grant exactly the three publish tools")
	}
	return policy, nil
}

// rejectServableMarkup refuses the media types a browser executes in the
// origin's own security context. The bucket sits behind a hostname that also
// carries Kai's other published work, so an uploader who can serve HTML or SVG
// as markup can script against that origin. SVG is the non-obvious one: it is
// an image everywhere else in this runtime and a scriptable document here.
func rejectServableMarkup(ct string) error {
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "text/html", "application/xhtml+xml", "image/svg+xml", "application/xml", "text/xml":
		return fmt.Errorf("mcp-beaver: content type %q executes in the origin's context and is refused", ct)
	}
	return nil
}

// s3IntArgument reads a positive integer from a KDL argument of either the Int
// or the String kind.
func s3IntArgument(v kdl.Value) (int64, error) {
	var parsed int64
	switch v.Kind() {
	case kdl.Int:
		parsed = int64(v.Int())
	case kdl.String:
		n, err := strconv.ParseInt(strings.TrimSpace(v.String()), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("mcp-beaver: `max-bytes` must be a positive integer, got %q", v.String())
		}
		parsed = n
	default:
		return 0, fmt.Errorf("mcp-beaver: `max-bytes` must be a positive integer, got a %s", v.Kind())
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("mcp-beaver: `max-bytes` must be a positive integer, got %d", parsed)
	}
	return parsed, nil
}
