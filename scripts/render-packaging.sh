#!/bin/sh
# Render the brew formula from dist/ binaries; requires an exact release tag so
# the URLs, the stamped version, and the hashes all name the same release.
set -e
VERSION="$(git describe --tags --exact-match)"
BARE="${VERSION#v}"
BASE="https://forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver/releases/download/${VERSION}"

sha() { shasum -a 256 "$1" | cut -d' ' -f1; }
DARWIN_ARM64="$(sha dist/mcp-beaver-darwin-arm64)"
DARWIN_AMD64="$(sha dist/mcp-beaver-darwin-amd64)"
LINUX_AMD64="$(sha dist/mcp-beaver-linux-amd64)"
LINUX_ARM64="$(sha dist/mcp-beaver-linux-arm64)"

cat > dist/mcp-beaver.rb <<FORMULA
class McpBeaver < Formula
  desc "A MCP server generator with a natural flow"
  homepage "https://forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver"
  version "${BARE}"
  license "MIT"

  on_macos do
    on_arm do
      url "${BASE}/mcp-beaver-darwin-arm64"
      sha256 "${DARWIN_ARM64}"
    end
    on_intel do
      url "${BASE}/mcp-beaver-darwin-amd64"
      sha256 "${DARWIN_AMD64}"
    end
  end
  on_linux do
    on_intel do
      url "${BASE}/mcp-beaver-linux-amd64"
      sha256 "${LINUX_AMD64}"
    end
    on_arm do
      url "${BASE}/mcp-beaver-linux-arm64"
      sha256 "${LINUX_ARM64}"
    end
  end

  def install
    bin.install Dir["mcp-beaver-*"].first => "mcp-beaver"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/mcp-beaver version")
  end
end
FORMULA

echo "dist/mcp-beaver.rb"
