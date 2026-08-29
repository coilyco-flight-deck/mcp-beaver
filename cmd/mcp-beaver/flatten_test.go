package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// flattenFixture writes a base guardfile and a child that inherits it, and
// returns the child's path.
func flattenFixture(t *testing.T) (dir, child string) {
	t.Helper()
	dir = t.TempDir()
	base := filepath.Join(dir, "base.mcp.kdl")
	if err := os.WriteFile(base, []byte(`
wrap ward mcp probe {
    base-url "https://probe.test/api/v1"
    auth header-token {
        header Authorization
        prefix "token "
        value env "PROBE_TOKEN"
    }
    can get thing {
        path "/things/{id}"
    }
}
`), 0o600); err != nil {
		t.Fatalf("write base: %v", err)
	}
	child = filepath.Join(dir, "tier.mcp.kdl")
	if err := os.WriteFile(child, []byte(`
wrap ward mcp probe {
    inherit "base.mcp.kdl"
    can list thing {
        path "/things"
    }
}
`), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}
	return dir, child
}

// TestFlattenResolvesInherit covers the artifact a runtime mounts: one
// self-contained document, carrying the parent's grants and no `inherit` left
// for a pod to follow.
func TestFlattenResolvesInherit(t *testing.T) {
	_, child := flattenFixture(t)
	var out bytes.Buffer
	if err := runFlatten(&out, []string{child}); err != nil {
		t.Fatalf("flatten: %v", err)
	}
	got := out.String()
	if !strings.HasPrefix(got, flattenBanner) {
		t.Error("flattened output does not lead with the generated banner")
	}
	if strings.Contains(got, "inherit") {
		t.Error("flattened output still carries an `inherit` a pod cannot resolve")
	}
	for _, want := range []string{"can get thing", "can list thing", "base-url", "auth header-token"} {
		if !strings.Contains(got, want) {
			t.Errorf("flattened output is missing %q", want)
		}
	}
}

// TestFlattenWritesAndChecks is the CI loop: generate, verify, then verify again
// after the source moves. A stale committed artifact is what gets mounted, so
// drift has to fail the build rather than ship.
func TestFlattenWritesAndChecks(t *testing.T) {
	dir, child := flattenFixture(t)
	artifact := filepath.Join(dir, "tier.flat.mcp.kdl")

	if err := runFlatten(os.Stdout, []string{child, "-o", artifact}); err != nil {
		t.Fatalf("flatten -o: %v", err)
	}
	if err := runFlatten(os.Stdout, []string{child, "-o", artifact, "--check"}); err != nil {
		t.Fatalf("--check failed on a freshly written artifact: %v", err)
	}

	if err := os.WriteFile(child, []byte(`
wrap ward mcp probe {
    inherit "base.mcp.kdl"
}
`), 0o600); err != nil {
		t.Fatalf("rewrite child: %v", err)
	}
	err := runFlatten(os.Stdout, []string{child, "-o", artifact, "--check"})
	if err == nil {
		t.Fatal("--check passed against a stale artifact")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error = %v, want it to name the drift", err)
	}
}

// TestFlattenCheckNeedsATarget keeps the flag pair honest: --check compares
// against a committed file, so it means nothing without one.
func TestFlattenCheckNeedsATarget(t *testing.T) {
	_, child := flattenFixture(t)
	if err := runFlatten(os.Stdout, []string{child, "--check"}); err == nil {
		t.Fatal("--check without -o was accepted")
	}
}

// TestFlattenPassesThroughASourceWithNoInherit keeps the command safe to run
// over every guardfile in a tree, not only the composed ones.
func TestFlattenPassesThroughASourceWithNoInherit(t *testing.T) {
	dir, _ := flattenFixture(t)
	var out bytes.Buffer
	if err := runFlatten(&out, []string{filepath.Join(dir, "base.mcp.kdl")}); err != nil {
		t.Fatalf("flatten: %v", err)
	}
	if !strings.Contains(out.String(), "can get thing") {
		t.Error("a source with no inherit did not survive the round trip")
	}
}
