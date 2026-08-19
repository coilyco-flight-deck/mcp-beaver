package mcpserver

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyPrefix namespaces every bucket, so this runtime's keys are identifiable
// in a store nothing else is expected to share but anything could.
const keyPrefix = "mcp-beaver:ratelimit:"

// bucketTTLFactor sets the key's expiry as a multiple of the refill window.
// TTL is why Redis was chosen over a table (deploy#549): a decommissioned
// server's key disappears on its own rather than waiting for a reaper.
const bucketTTLFactor = 2

// minBucketTTL floors the expiry, so a sub-second politeness rate does not
// produce a key that expires between two calls of one conversation.
const minBucketTTL = time.Minute

// redisWaitPoll bounds one blocking wait before the bucket re-asks the store,
// so a bucket refilled by ANOTHER pod is noticed rather than slept through.
const redisWaitPoll = time.Second

// tokenBucketScript is the whole algorithm, run atomically inside Redis so two
// pods cannot both read the same token count and both spend it.
//
// It is a continuously-refilling token bucket rather than a fixed window,
// matching the in-memory limiter it replaces: burst equals count, and the
// allowance returns gradually rather than all at once on a window boundary.
// The retry delay comes back as a string because Redis truncates a Lua number
// to an integer, which would round every sub-second wait to zero.
const tokenBucketScript = `
local capacity = tonumber(ARGV[1])
local refill   = tonumber(ARGV[2])
local now      = tonumber(ARGV[3])
local ttl      = tonumber(ARGV[4])

local state  = redis.call('HMGET', KEYS[1], 'tokens', 'ts')
local tokens = tonumber(state[1])
local ts     = tonumber(state[2])
if tokens == nil or ts == nil then
  tokens = capacity
  ts = now
end

if now > ts then
  tokens = math.min(capacity, tokens + (now - ts) * refill)
  ts = now
end

local allowed = 0
local retry = 0
if tokens >= 1 then
  tokens = tokens - 1
  allowed = 1
else
  retry = (1 - tokens) / refill
end

redis.call('HSET', KEYS[1], 'tokens', tokens, 'ts', ts)
redis.call('EXPIRE', KEYS[1], ttl)
return {allowed, tostring(retry)}
`

// redisBucket is a durable token bucket. Every call is a round trip: there is
// no local cache, because a cached allowance is exactly the bug this fixes.
type redisBucket struct {
	client   *redis.Client
	script   *redis.Script
	key      string
	capacity int
	refill   float64 // tokens per second
	ttl      int
}

// newRedisBucket resolves the store URL and builds the client. It does not
// dial: a store that is down at boot must not stop a pod from starting, and
// the first call reports the outage as a tool error instead.
func newRedisBucket(cfg *rateLimitConfig, key string) (*redisBucket, error) {
	url, err := resolveStoreURL(cfg.store)
	if err != nil {
		return nil, err
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		// The URL carries the password, so the parse error is reported by
		// provider and address rather than by value.
		return nil, fmt.Errorf("mcp-beaver: `rate-limit` store URL from %s %q is not a redis URL", cfg.store.urlProvider, cfg.store.urlAddress)
	}
	window := cfg.window.Seconds()
	ttl := int(math.Ceil(window * bucketTTLFactor))
	if min := int(minBucketTTL.Seconds()); ttl < min {
		ttl = min
	}
	return &redisBucket{
		client:   redis.NewClient(opts),
		script:   redis.NewScript(tokenBucketScript),
		key:      keyPrefix + key,
		capacity: cfg.count,
		refill:   float64(cfg.count) / window,
		ttl:      ttl,
	}, nil
}

// resolveStoreURL reads the store URL from the declared value source.
func resolveStoreURL(store *redisStore) (string, error) {
	switch store.urlProvider {
	case "literal":
		return store.urlAddress, nil
	case "env":
		v, ok := os.LookupEnv(store.urlAddress)
		if !ok || v == "" {
			return "", fmt.Errorf("mcp-beaver: `rate-limit` store env %q is not set", store.urlAddress)
		}
		return v, nil
	}
	return "", fmt.Errorf("mcp-beaver: `rate-limit` store provider %q is not supported", store.urlProvider)
}

// Wait charges one token, blocking only while the projected wait fits inside
// the request deadline.
//
// A store outage is a REFUSAL, never a fallback to an in-memory bucket. Every
// spec naming a store opted in because its upstream is metered, and quietly
// serving without the bound would remove the only thing standing between a
// blip and the spend it was declared to cap (mcp-beaver#69).
func (b *redisBucket) Wait(ctx context.Context) error {
	for {
		allowed, retry, err := b.charge(ctx)
		if err != nil {
			return fmt.Errorf("rate-limit store unreachable, refusing rather than serving unbounded: %w", err)
		}
		if allowed {
			return nil
		}
		if err := b.sleep(ctx, retry); err != nil {
			return err
		}
	}
}

// charge runs one atomic attempt against the store.
func (b *redisBucket) charge(ctx context.Context) (bool, time.Duration, error) {
	now := float64(time.Now().UnixNano()) / float64(time.Second)
	out, err := b.script.Run(ctx, b.client, []string{b.key},
		b.capacity, b.refill, now, b.ttl).Slice()
	if err != nil {
		return false, 0, err
	}
	if len(out) != 2 {
		return false, 0, fmt.Errorf("bucket script returned %d values, want 2", len(out))
	}
	allowed, _ := out[0].(int64)
	retrySeconds := 0.0
	if s, ok := out[1].(string); ok {
		retrySeconds, _ = strconv.ParseFloat(s, 64)
	}
	return allowed == 1, time.Duration(retrySeconds * float64(time.Second)), nil
}

// sleep waits out a refill, refusing as soon as the projected wait exceeds the
// request deadline rather than blocking to it. Preserving that fast fail is an
// explicit requirement: a spent budget is a reportable tool error in a fifth
// of a second, not a hung turn.
func (b *redisBucket) sleep(ctx context.Context, retry time.Duration) error {
	deadline, ok := ctx.Deadline()
	if ok && time.Now().Add(retry).After(deadline) {
		return fmt.Errorf("rate: Wait(n=1) would exceed context deadline (bucket refills in %s)", retry.Round(time.Millisecond))
	}
	if retry > redisWaitPoll {
		retry = redisWaitPoll
	}
	timer := time.NewTimer(retry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Close releases the store connection. A served pod holds one bucket for its
// lifetime, so this exists for tests and for a clean shutdown.
func (b *redisBucket) Close() error { return b.client.Close() }
