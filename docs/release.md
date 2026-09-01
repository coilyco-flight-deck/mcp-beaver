# Releasing mcp-beaver

Two artifacts leave this repo on two different cadences, and they are not the
same thing.

- **The OCI image**, published by `ci.yml` on every main push and keyed by the
  full source sha. It is how the fleet runs beaver, and `deploy` consumes that
  exact reference. See [image.md](image.md) and [ci.md](ci.md).
- **The binaries**, published by `release.yml` when a revision changes what the
  binary ships. It is how a person installs beaver. This page.

## Installing

```sh
brew tap coilyco-flight-deck/tap https://forgejo.coilysiren.me/coilyco-flight-deck/homebrew-tap.git
brew install mcp-beaver
```

macOS on Apple Silicon or Intel, and Linux on x86-64 or arm64. Every binary is
static, `CGO_ENABLED=0`, so it needs nothing installed beside it.

Or take one from the [releases
page](https://forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver/releases)
directly, with `SHA256SUMS` beside it:

```sh
curl -fsSLO https://forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver/releases/latest/download/mcp-beaver-linux-amd64
chmod +x mcp-beaver-linux-amd64
./mcp-beaver-linux-amd64 version
```

## What the train does

`release.yml` runs on every push to `main`, and every run validates with
`just test`. Whether it publishes is a second question, answered by
`just release-impact`:

- A revision that touches `cmd/`, `internal/`, `go.mod`, `go.sum`, or either
  release script publishes.
- Anything else validates and stops, so a docs or chart commit never mints a
  version indistinguishable from the one before it.
- The comparison runs from the previous release tag rather than the pushed
  range, so a shipped change that follows an unpublished revision still
  publishes rather than being skipped with it.

When it publishes: the tag is bumped, four binaries are cross-compiled and
checksummed, a Forgejo release is created and the artifacts attached, and the
Homebrew formula is pushed to the tap. The tag and release steps are
agentic-os's shared composite actions, consumed by ref rather than forked.

## Versions

Every automatic release is a **minor** bump. Nothing in commit history can
escalate that, by design. A **major** version is hand-driven: dispatch the
workflow with `bump: major`.

```sh
aosguard ops forgejo workflow dispatch coilyco-flight-deck mcp-beaver \
    release.yml --ref main --inputs '{"bump":"major"}'
```

A dispatch always publishes, whatever the impact classifier would have said, so
it is also the retry path when a run failed after tagging. Pass an explicit
`tag` to retry against a tag that already exists.

While a `.release-major` file is present, a push validates and stops rather
than publishing, so a minor cannot take the next number while a major is
pending. A dispatch ignores the hold, which is how the major gets cut. Delete
the file once it is out.

## The version stamp

`internal/mcpserver.Version` is `dev` in every build the release did not
produce, and the release build stamps the tag into it with `-X`. One stamp
target for the whole runtime, so `mcp-beaver version` and the version every MCP
handshake advertises in `serverInfo` can never disagree about which build is
running.

## See also

- [ci.md](ci.md) - the gate and the image publish.
- [image.md](image.md) - the runtime image the chart deploys.
