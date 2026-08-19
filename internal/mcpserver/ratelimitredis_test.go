package mcpserver

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// testRedisURL names a live Redis for the durable-bucket tests. They are
// skipped rather than faked when it is unset: a fake store cannot show that a
// bucket survives a restart, which is the entire claim (deploy#549).
const testRedisEnv = "MCP_BEAVER_TEST_REDIS_URL"

func redisURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv(testRedisEnv)
	if url == "" {
		t.Skipf("set %s to a live Redis to run the durable-bucket tests", testRedisEnv)
	}
	return url
}

// durableBucket builds a bucket against the live store under a key unique to
// this test, so a rerun never inherits a spent budget from the last one.
func durableBucket(t *testing.T, key, spec string) rateBucket {
	t.Helper()
	count, window, err := parseRateSpec(spec)
	if err != nil {
		t.Fatalf("parseRateSpec: %v", err)
	}
	bucket, err := newRateBucket(&rateLimitConfig{
		count:  count,
		window: window,
		store:  &redisStore{urlProvider: "literal", urlAddress: redisURL(t)},
		bucket: key,
	}, "unused-server-name")
	if err != nil {
		t.Fatalf("newRateBucket: %v", err)
	}
	return bucket
}

// uniqueKey keeps a rerun from inheriting a spent budget. Setting
// MCP_BEAVER_TEST_BUCKET_KEY pins it instead, which is what makes the
// store-restart acceptance runnable in two phases around a restart of the
// STORE itself - the run deploy#549 requires, since restarting only the
// process would pass while the bug survives in a memory-only Redis.
func uniqueKey(t *testing.T) string {
	t.Helper()
	if pinned := os.Getenv("MCP_BEAVER_TEST_BUCKET_KEY"); pinned != "" {
		return pinned
	}
	return fmt.Sprintf("test-%s-%d", t.Name(), time.Now().UnixNano())
}

// TestSpendTheBudget is phase one of the store-restart acceptance: it spends a
// pinned budget and asserts nothing more is served. Phase two is
// TestBudgetIsStillSpent, run after the store restarts.
func TestSpendTheBudget(t *testing.T) {
	if os.Getenv("MCP_BEAVER_TEST_BUCKET_KEY") == "" {
		t.Skip("set MCP_BEAVER_TEST_BUCKET_KEY to run the store-restart acceptance")
	}
	bucket := durableBucket(t, uniqueKey(t), "2/24h")
	for i := range 2 {
		if err := bucket.Wait(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if err := waitBriefly(bucket); err == nil {
		t.Fatalf("a spent budget served a third call")
	}
}

// TestBudgetIsStillSpent is phase two: the STORE has restarted since phase
// one, so a refilled budget here means the append-only persistence is not
// doing its job and the bug moved one layer down.
func TestBudgetIsStillSpent(t *testing.T) {
	if os.Getenv("MCP_BEAVER_TEST_BUCKET_KEY") == "" {
		t.Skip("set MCP_BEAVER_TEST_BUCKET_KEY to run the store-restart acceptance")
	}
	bucket := durableBucket(t, uniqueKey(t), "2/24h")
	if err := waitBriefly(bucket); err == nil {
		t.Fatalf("the budget refilled across a STORE restart")
	}
}

// The bug: a budget returns to full whenever the process does. A new bucket
// over the same key is exactly what a pod roll produces.
func TestDurableBucketSurvivesAProcessRestart(t *testing.T) {
	key := uniqueKey(t)
	first := durableBucket(t, key, "2/24h")
	for i := range 2 {
		if err := first.Wait(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if err := waitBriefly(first); err == nil {
		t.Fatalf("a spent budget served a third call")
	}

	// A brand-new bucket is a restarted process: same key, no shared memory.
	restarted := durableBucket(t, key, "2/24h")
	if err := waitBriefly(restarted); err == nil {
		t.Fatalf("the budget refilled across a restart, which is the bug")
	}
}

// Two pods rendering one guardfile charge ONE budget. Losing this doubles
// every figure silently, which deploy#549 measured as worth as much as
// durability itself.
func TestDurableBucketIsSharedAcrossPods(t *testing.T) {
	key := uniqueKey(t)
	echo := durableBucket(t, key, "2/24h")
	deep := durableBucket(t, key, "2/24h")

	if err := echo.Wait(context.Background()); err != nil {
		t.Fatalf("echo first call: %v", err)
	}
	if err := deep.Wait(context.Background()); err != nil {
		t.Fatalf("deep first call: %v", err)
	}
	// Two calls across two pods spent the whole 2/24h budget.
	if err := waitBriefly(echo); err == nil {
		t.Fatalf("echo served a third call, so the pods hold separate budgets")
	}
	if err := waitBriefly(deep); err == nil {
		t.Fatalf("deep served a third call, so the pods hold separate budgets")
	}
}

// A deliberate split still works, which is why `bucket` exists as an override.
func TestDifferentBucketKeysDoNotShare(t *testing.T) {
	base := uniqueKey(t)
	one := durableBucket(t, base+"-a", "1/24h")
	two := durableBucket(t, base+"-b", "1/24h")

	if err := one.Wait(context.Background()); err != nil {
		t.Fatalf("first bucket: %v", err)
	}
	if err := two.Wait(context.Background()); err != nil {
		t.Fatalf("a separate key was charged by the first bucket: %v", err)
	}
}

// A spent budget is a fast reportable error rather than a hung turn. The 0.19s
// behaviour of the in-memory limiter is an explicit requirement to preserve.
func TestDurableBucketFailsFastOnASpentBudget(t *testing.T) {
	bucket := durableBucket(t, uniqueKey(t), "1/24h")
	if err := bucket.Wait(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := bucket.Wait(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("a spent budget served a call")
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("took %s to refuse, want a fast fail rather than a block to the deadline", elapsed)
	}
}

// The allowance returns gradually rather than all at once, matching the
// in-memory limiter this replaces.
func TestDurableBucketRefills(t *testing.T) {
	bucket := durableBucket(t, uniqueKey(t), "2/1s")
	for i := range 2 {
		if err := bucket.Wait(context.Background()); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := bucket.Wait(ctx); err != nil {
		t.Fatalf("the bucket never refilled: %v", err)
	}
}

// A store outage REFUSES rather than falling back. Every spec naming a store
// opted in because its upstream is metered, so serving unbounded would remove
// the only thing capping the spend.
func TestUnreachableStoreRefusesRatherThanServing(t *testing.T) {
	bucket, err := newRateBucket(&rateLimitConfig{
		count: 5, window: time.Hour,
		store:  &redisStore{urlProvider: "literal", urlAddress: "redis://127.0.0.1:1"},
		bucket: "unreachable",
	}, "server")
	if err != nil {
		t.Fatalf("newRateBucket: %v", err)
	}
	called := 0
	handler := withRateLimit(bucket, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		called++
		return &mcp.CallToolResult{}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := handler(ctx, &mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("an unreachable store served the call")
	}
	if called != 0 {
		t.Fatalf("the upstream was reached %d times with no bucket behind it", called)
	}
}

// A guardfile with no store keeps today's in-memory bucket exactly, so the
// keyless servers already deployed are untouched.
func TestNoStoreKeepsTheInMemoryBucket(t *testing.T) {
	bucket, err := newRateBucket(&rateLimitConfig{count: 1, window: time.Hour}, "server")
	if err != nil {
		t.Fatalf("newRateBucket: %v", err)
	}
	if _, ok := bucket.(memoryBucket); !ok {
		t.Fatalf("bucket = %T, want the in-memory one", bucket)
	}
}

func TestRateLimitBlockFailsClosed(t *testing.T) {
	for name, node := range map[string]string{
		"unknown child":    "rate-limit \"1/1s\" {\n    backing redis env \"X\"\n}",
		"unknown driver":   "rate-limit \"1/1s\" {\n    store memcached env \"X\"\n}",
		"unknown provider": "rate-limit \"1/1s\" {\n    store redis vault \"X\"\n}",
		"store arity":      "rate-limit \"1/1s\" {\n    store redis\n}",
		"empty address":    "rate-limit \"1/1s\" {\n    store redis env \"\"\n}",
		"duplicate store":  "rate-limit \"1/1s\" {\n    store redis env \"A\"\n    store redis env \"B\"\n}",
		"bucket no store":  "rate-limit \"1/1s\" {\n    bucket \"shared\"\n}",
		"duplicate bucket": "rate-limit \"1/1s\" {\n    store redis env \"A\"\n    bucket \"a\"\n    bucket \"b\"\n}",
		"unset store env":  "rate-limit \"1/1s\" {\n    store redis env \"MCP_BEAVER_DEFINITELY_UNSET\"\n}",
		"not a redis url":  "rate-limit \"1/1s\" {\n    store redis literal \"not-a-url\"\n}",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New("test", "test.mcp.kdl", []byte(node+"\n\n"+roundTripSpec("http://127.0.0.1:1")))
			if err == nil {
				t.Fatal("New accepted a malformed durable `rate-limit`")
			}
		})
	}
}

// waitBriefly asks for a token under a short deadline, so an exhausted bucket
// reports rather than blocking the test for the refill window.
func waitBriefly(bucket rateBucket) error {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	return bucket.Wait(ctx)
}
