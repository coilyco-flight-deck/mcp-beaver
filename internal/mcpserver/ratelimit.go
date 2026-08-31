package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	kdl "github.com/calico32/kdl-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// parseRateLimit reads the optional top-level `rate-limit` node, a sibling of
// `wrap`:
//
//	rate-limit "1/1s"     // one request per second, the MusicBrainz case
//	rate-limit "10/1m"    // ten per minute
//	rate-limit "72/24h" { store redis env "REDIS_URL" }   // a spend budget
//
// The bucket is per server and process-wide, which is the shape the consuming
// deployment needs: the pod has one IP, so a public-good API sees one caller
// whose request rate is the sum of a whole community's curiosity. Per-grant
// granularity is deliberately not offered - the upstream publishes one limit,
// not one per endpoint.
//
// Stated beside `wrap` rather than inside it. The issue asking for this
// sketched it as a wrap-body node, but that body is opcore's frozen grammar
// and the umbra pin, so a sibling keeps the runtime's half of the change here
// where it belongs.
func parseRateLimit(sources []guardSource) (*rateLimitConfig, error) {
	nodes, err := parseInlineNodes(sources, "rate-limit")
	if err != nil {
		return nil, err
	}
	var cfg *rateLimitConfig
	for _, sn := range nodes {
		n := sn.node
		if n.Name() != "rate-limit" {
			continue
		}
		if cfg != nil {
			return nil, fmt.Errorf("mcp-beaver: duplicate `rate-limit` node")
		}
		if len(n.Properties()) > 0 {
			return nil, fmt.Errorf("mcp-beaver: `rate-limit` takes no properties, only a rate argument like \"1/1s\" (fail-closed)")
		}
		spec, err := oneStringArg(n, "rate-limit")
		if err != nil {
			return nil, err
		}
		count, window, err := parseRateSpec(spec)
		if err != nil {
			return nil, err
		}
		cfg = &rateLimitConfig{count: count, window: window}
		if err := parseRateLimitBlock(n, cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// rateLimitConfig is the parsed node: the rate itself, plus where the bucket
// lives. A nil store keeps the in-memory limiter every keyless spec has today.
type rateLimitConfig struct {
	count  int
	window time.Duration
	store  *redisStore
	bucket string
}

// redisStore is a declared durable backing store: the driver name is fixed at
// redis today, and the URL resolves from a value source at build.
type redisStore struct {
	urlProvider string
	urlAddress  string
}

// parseRateLimitBlock reads the optional `{ store ...; bucket ... }` body.
func parseRateLimitBlock(n *kdl.Node, cfg *rateLimitConfig) error {
	for _, c := range n.Children().Nodes {
		switch c.Name() {
		case "store":
			if cfg.store != nil {
				return fmt.Errorf("mcp-beaver: duplicate `store` in `rate-limit` (fail-closed)")
			}
			store, err := parseRateLimitStore(c)
			if err != nil {
				return err
			}
			cfg.store = store
		case "bucket":
			if cfg.bucket != "" {
				return fmt.Errorf("mcp-beaver: duplicate `bucket` in `rate-limit` (fail-closed)")
			}
			v, err := oneStringArg(c, "bucket")
			if err != nil {
				return err
			}
			cfg.bucket = v
		default:
			return fmt.Errorf("mcp-beaver: unknown `rate-limit` child %q (want store | bucket; fail-closed)", c.Name())
		}
	}
	if cfg.bucket != "" && cfg.store == nil {
		return fmt.Errorf("mcp-beaver: `bucket` names a shared key but no `store` backs it, so it would be a private in-memory bucket (fail-closed)")
	}
	return nil
}

// parseRateLimitStore reads `store redis <provider> "<address>"`.
func parseRateLimitStore(c *kdl.Node) (*redisStore, error) {
	args := c.Arguments()
	if len(args) != 3 {
		return nil, fmt.Errorf("mcp-beaver: `store` wants a driver, a provider, and an address, e.g. `store redis env \"REDIS_URL\"` (got %d)", len(args))
	}
	driver, provider, address := args[0].String(), args[1].String(), args[2].String()
	if driver != "redis" {
		return nil, fmt.Errorf("mcp-beaver: `store` driver %q is not supported (want redis; fail-closed)", driver)
	}
	switch provider {
	case "env", "literal":
	default:
		return nil, fmt.Errorf("mcp-beaver: `store` provider %q is not supported (want env | literal; fail-closed)", provider)
	}
	if address == "" {
		return nil, fmt.Errorf("mcp-beaver: `store` needs a non-empty address")
	}
	return &redisStore{urlProvider: provider, urlAddress: address}, nil
}

// parseRateSpec reads the `<count>/<duration>` form. Burst equals count, so
// "10/1m" permits ten immediately and then refills, while "1/1s" is strict
// serialisation - which is what a 1 req/sec published limit actually means.
func parseRateSpec(spec string) (int, time.Duration, error) {
	count, window, found := strings.Cut(strings.TrimSpace(spec), "/")
	if !found {
		return 0, 0, fmt.Errorf("mcp-beaver: `rate-limit` %q must be <count>/<duration>, e.g. \"1/1s\"", spec)
	}
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || n <= 0 {
		return 0, 0, fmt.Errorf("mcp-beaver: `rate-limit` %q needs a positive integer count", spec)
	}
	d, err := time.ParseDuration(strings.TrimSpace(window))
	if err != nil || d <= 0 {
		return 0, 0, fmt.Errorf("mcp-beaver: `rate-limit` %q needs a positive duration, e.g. 1s or 1m", spec)
	}
	return n, d, nil
}

// rateBucket is the thing a call charges. Two implementations: the in-memory
// limiter every keyless spec has always had, and a Redis-backed durable one.
type rateBucket interface {
	Wait(ctx context.Context) error
}

// memoryBucket is today's behaviour, unchanged: a process-local token bucket
// that resets whenever the process does.
type memoryBucket struct{ limiter *rate.Limiter }

func (b memoryBucket) Wait(ctx context.Context) error { return b.limiter.Wait(ctx) }

// newRateBucket builds the bucket the config asks for. serverName is the
// default bucket key, so two pods rendering the same guardfile charge ONE
// budget rather than one each - the doubling deploy#549 measured.
func newRateBucket(cfg *rateLimitConfig, serverName string) (rateBucket, error) {
	if cfg == nil {
		return nil, nil
	}
	limiter := rate.NewLimiter(rate.Every(cfg.window/time.Duration(cfg.count)), cfg.count)
	if cfg.store == nil {
		return memoryBucket{limiter: limiter}, nil
	}
	key := cfg.bucket
	if key == "" {
		key = serverName
	}
	return newRedisBucket(cfg, key)
}

// withRateLimit serialises a tool call against the server's bucket.
//
// It waits rather than rejecting. A queued tool call is slower; a 503 is a
// failed turn, and the caller here answers in a shared channel where a failed
// turn is user-visible. Waiting is bounded for free by the per-request
// deadline: a call that would queue past it fails with a stated timeout rather
// than holding a slot indefinitely.
//
// Applied only to grant-backed tools. The info tool and withheld stubs reach
// no upstream, so charging them against an upstream's bucket would throttle
// the fleet's liveness probe on behalf of a service it never calls.
func withRateLimit(bucket rateBucket, next mcp.ToolHandler) mcp.ToolHandler {
	if bucket == nil {
		return next
	}
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := bucket.Wait(ctx); err != nil {
			return toolError(fmt.Errorf("mcp-beaver: rate limit wait: %w", err)), nil
		}
		return next(ctx, req)
	}
}
