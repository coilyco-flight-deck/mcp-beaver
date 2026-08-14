package mcpserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/time/rate"
)

// parseRateLimit reads the optional top-level `rate-limit` node, a sibling of
// `wrap`:
//
//	rate-limit "1/1s"     // one request per second, the MusicBrainz case
//	rate-limit "10/1m"    // ten per minute
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
func parseRateLimit(src []byte) (*rate.Limiter, error) {
	doc, err := parseInlineDoc(src, "rate-limit")
	if err != nil {
		return nil, err
	}
	var limiter *rate.Limiter
	for _, n := range doc.Nodes {
		if n.Name() != "rate-limit" {
			continue
		}
		if limiter != nil {
			return nil, fmt.Errorf("ward-mcp: duplicate `rate-limit` node")
		}
		if len(n.Properties()) > 0 {
			return nil, fmt.Errorf("ward-mcp: `rate-limit` takes no properties, only a rate argument like \"1/1s\" (fail-closed)")
		}
		spec, err := oneStringArg(n, "rate-limit")
		if err != nil {
			return nil, err
		}
		limiter, err = newRateLimiter(spec)
		if err != nil {
			return nil, err
		}
	}
	return limiter, nil
}

// newRateLimiter reads the `<count>/<duration>` form. Burst equals count, so
// "10/1m" permits ten immediately and then refills, while "1/1s" is strict
// serialisation - which is what a 1 req/sec published limit actually means.
func newRateLimiter(spec string) (*rate.Limiter, error) {
	count, window, found := strings.Cut(strings.TrimSpace(spec), "/")
	if !found {
		return nil, fmt.Errorf("ward-mcp: `rate-limit` %q must be <count>/<duration>, e.g. \"1/1s\"", spec)
	}
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("ward-mcp: `rate-limit` %q needs a positive integer count", spec)
	}
	d, err := time.ParseDuration(strings.TrimSpace(window))
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("ward-mcp: `rate-limit` %q needs a positive duration, e.g. 1s or 1m", spec)
	}
	return rate.NewLimiter(rate.Every(d/time.Duration(n)), n), nil
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
func withRateLimit(limiter *rate.Limiter, next mcp.ToolHandler) mcp.ToolHandler {
	if limiter == nil {
		return next
	}
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if err := limiter.Wait(ctx); err != nil {
			return toolError(fmt.Errorf("ward-mcp: rate limit wait: %w", err)), nil
		}
		return next(ctx, req)
	}
}
