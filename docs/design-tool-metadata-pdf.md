# In-process PDF extraction

Why mcp-beaver extracts PDF text inside the process rather than shelling out. Split from the derived-tool-metadata page.

### PDF extraction: in-process, and why (mcp-beaver#60)

A large amount of authoritative reference material - government statistics,
standards bodies, scientific supplementary material, regulatory filings,
equipment documentation - is published **only** as PDF, with no JSON API and
often no HTML equivalent. A grant that reaches one returned bytes an agent
could not read, so those sources were invisible to the whole fleet.

`extract "<tool>" as="pdf-text" max-pages="20"` turns the document into text.
The projection lives here rather than in the guardfile grammar because turning
an upstream response into tool content is this runtime's half of the boundary:
umbra owns guarded execution, mcp-beaver owns projection.

**Isolation posture: in-process, decided rather than defaulted.** The issue's
strongest argument for a sidecar was memory safety, since PDF parsers have a
long history of memory-safety and resource-exhaustion vulnerabilities and this
one parses arbitrary internet documents inside a pod holding upstream
credentials. That argument is about **C** parsers - poppler, mupdf. The parser
here is pure Go, so a malformed document cannot corrupt memory or escape the
type system, and the residual risks are all boundable in process:

* **Size.** `maxPDFBytes` (32MB) gates before the parser opens anything.
* **Pages.** Bounded, defaulting to 20, ceilinged at 200. Returning a whole
  document's text would blow any agent's context, so the bound is the default
  and the guardfile raises it rather than the reverse.
* **Time.** The parse runs on its own goroutine with the caller selecting on
  the request context, so a wedged document returns a stated timeout rather
  than holding the handler - mcp-beaver#49 recorded a 180s hang inside two
  healthy pods, and a slow parse is that shape. The abandoned goroutine is the
  deliberate trade: it is bounded by the size gate and the pod's memory limit,
  where a blocked handler is bounded by nothing.
* **Panics.** Recovered into a tool error. A pure-Go parser still panics on
  structures it does not handle, and a served pod must not die of a document
  someone linked.

A sidecar would have added an image, a pod, and a second egress path to reduce
a risk class the language already removes. It stays the right answer if OCR
lands, since that dependency footprint is genuinely different.

**Library: `github.com/dslipak/pdf`, chosen on measured output.**
`rsc.io/pdf` is the better provenance - Go Authors, tiny, zero dependencies -
and it was tried first. On a real-world PDF using an embedded subset font it
returned mojibake, rendering "Copyright" as `?#-5$+8*)`, because it does not
apply the font's ToUnicode CMap. That is the worst available failure: text that
looks like a successful extraction and is wrong. The dslipak fork carries the
same Go Authors BSD license and the same small pure-Go surface, applies the
CMap, and returned correct prose on the same document. Both were run before
choosing; the decision is measurement rather than provenance.

**Scope: text only.** Text extraction, table extraction, and OCR are three very
different amounts of work with three very different dependency footprints, and
text alone covers most of the stated need. `as="pdf-text"` names the extraction
so the next one is additive rather than a rewrite.

**Bounded reads say so.** The coverage block gains `pages: {shown, total}`.
This is the one place the runtime can honestly state a shown-of-total, because
the bound is its own rather than the upstream's - a model told only that it
received text has no way to know page 21 exists.

**Requires `raw-response`.** Without it opcore decodes the body as JSON and the
call fails on the first byte of `%PDF`, long before extraction runs. Declaring
`extract` on a grant that omits it is a build error rather than a call-time
surprise.

## See also

- [design-tool-metadata.md](design-tool-metadata.md) - derived tool metadata.
- [DESIGN.md](DESIGN.md) - the index.
