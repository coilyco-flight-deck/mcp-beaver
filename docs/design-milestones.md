# Milestones

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## First milestone (matches deploy#40)

Narrowest end-to-end slice: the `forgejo-issues` guardfile, one grant (`can create issue`), built to an image that serves `create_issue` over the SDK-backed `/mcp` transport. deploy#40 then stands that image up as a tailnet-only k3s service an agent mounts by URL. Everything else (comment, list, close, other upstreams) is additive once the spine works, and needs no new grammar.

## See also

- [design-foundations.md](design-foundations.md)
- [design-tool-metadata.md](design-tool-metadata.md)
- [design-pipeline-and-distribution.md](design-pipeline-and-distribution.md)
- [design-spec-dialect.md](design-spec-dialect.md)
- [DESIGN.md](DESIGN.md) - the index.
