# The safety story and derived tool metadata

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The safety story - the guardfile IS the tool surface

A mcp-beaver server **cannot exceed its guardfile**, because the guardfile is the only source of the tools.

* Every `can` grant is one MCP tool and one matching HTTP endpoint. There is no way to declare a tool the guardfile does not grant.
* An unwritten `delete issue` grant means no `delete_issue` tool is minted. In the frozen `opcore.ParseInline` grammar this is **deny-by-absence**: the served surface is exactly the `can` grants, so leaving a grant out is the deletion guard.
* `restrict owner matches coilyco-*` bounds every `{owner}` path param.
* The argv metachar gate runs on inputs that compose into the URL (path + query); body fields (issue titles, markdown) are exempt, as in umbra.

Handing a write-capable MCP to an agent (deploy#40's ask) is defensible: the blast radius is one small reviewable file, enforced at the transport, not trusted to the model.

## Derived tool metadata

The guardfile remains the sole per-tool authoring surface. mcp-beaver derives the
MCP metadata that clients use for selection, display, and confirmation:

* **Title** - a human-readable form of `verb_resource`, such as `Create issue`.
* **Description** - the grant's `describe` note when present. Otherwise a
  user-goal sentence derived from the verb and resource.
* **Safety annotations** - GET, HEAD, and OPTIONS are read-only. The standard
  HTTP idempotent methods are marked idempotent. umbra's destructive grant
  bit controls `destructiveHint`. Local operations are open-world because they
  call a configured upstream service.
* **Output schema** - generic HTTP operations advertise an object with one
  required `result` field. The field accepts the decoded upstream JSON value or
  the fallback response text. `tools/call` returns that object in
  `structuredContent` and mirrors the original response in text content for
  older clients.
* **Server instructions** - initialize includes compact guidance about the
  policy-approved surface, reading before mutation, and treating annotations as
  hints rather than authorization.

The exact-parameter SSM reader advertises the same read-only safety metadata and
a specific parameter result schema. An allowlisted upstream proxy preserves the
upstream tool contract, including its title, schemas, annotations, and result,
instead of reclassifying behavior mcp-beaver does not own.

### Coverage before payload (mcp-beaver#68)

A consuming harness bounds a tool result at a byte cap, and the common shape is
a **head slice**: keep the front, discard the tail, append a byte-count notice.
So whichever field serializes last is the first one destroyed, deterministically
and silently. sirens-echo#449 cost a day to a server that put its `warnings` key
last: the surface said it had searched 528 of 22,933 rows, the reply said none
existed, and only the first was true.

Two properties make it worse than an ordinary truncation bug. The notice is in
**bytes, not meaning** - the model learns "this was cut" and never learns "the
search covered 528 of 22,933 rows", and only the second changes an answer. And
the cap is **per-consumer**: 8192 on one profile and 16384 on another, so the
same query is honest for one reader and silently caveat-free for the other, with
nothing in either log saying so.

mcp-beaver generates the fleet's servers, so the property is worth fixing once
here rather than once per server.

**Enforced by the runtime, not left to the guardfile:**

* **Coverage serializes first.** Every grant-backed result is
  `{"coverage": {...}, "result": ...}`, and the envelope is a Go **struct**
  rather than a map - `encoding/json` writes struct fields in declaration order
  and map keys in sorted order, so a struct is what makes the position a
  contract instead of an accident of the next field's name.
* **The text content carries the same envelope.** Text and structured content
  used to disagree - text was the bare upstream body, structured was
  `{"result": ...}` - and the text half is the one a consumer slices. Leading
  the structured half alone would have put the caveat exactly where it always
  got destroyed.
* **Truncation is stated, never silent.** `truncated` is always present and
  always false, because nothing here truncates. The standing explicit claim is
  what lets a consumer attribute a short view to its own slicing.
* **Arrays are counted by name.** `coverage.items` names every array in the
  payload and its length. This is where the eco-app#267 shape - 45 KB at
  `limit=1`, because `limit` bounded one array of six - becomes visible on the
  first call instead of after an investigation.
* **`over_budget` marks a response past 8192 bytes**, the smallest consumer cap
  measured. It does not mean this runtime cut anything. It means some consumer
  will, and the model may be reading a prefix.

**Not enforceable here, stated so an author is not surprised:**

* **`limit` bounding every array is the upstream's job.** `limit` is a query
  parameter this runtime forwards; nothing here can make an upstream bound a
  second array it chose not to. What the runtime does instead is refuse to let
  that stay invisible - an unbounded array shows up as a named count and an
  `over_budget` flag rather than as a response that merely feels large.
* **Null-versus-zero is the upstream's word.** This runtime never synthesizes a
  zero: an upstream it cannot read is an `isError` tool result, and a decoded
  `null` stays `null`. An upstream that answers a failed read with `0` is
  reporting its own state, and no envelope can tell that apart from a measured
  zero.
* **Upstream-proxy mode passes the upstream's own result through.** The whole
  point of that mode is preserving the upstream contract, so the envelope there
  is the upstream's to shape. `lint-upstream` is where an allowlisted surface
  gets reviewed.
* **Fixed-shape tools carry no coverage block.** `serve-ssm` returns one
  parameter, `mcp_beaver_info` returns the server's own shape, and a withheld
  stub returns a refusal. None can grow with upstream data, and a coverage
  block on a response that cannot be partial would train a reader to skim it.

## See also

- [design-tool-metadata-pdf.md](design-tool-metadata-pdf.md) - in-process PDF extraction.
- [design-tool-metadata-sessions.md](design-tool-metadata-sessions.md) - upstream session reuse.
- [DESIGN.md](DESIGN.md) - the index.
