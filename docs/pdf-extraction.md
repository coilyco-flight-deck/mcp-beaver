# PDF text extraction

`extract "<tool-name>" as="pdf-text" max-pages="20"` turns a PDF an upstream
returns into text an agent can read. A lot of authoritative reference material,
including government statistics, standards bodies, regulatory filings, and
equipment documentation, is published only as PDF, so a grant reaching one used
to return bytes no model could use.

Requires the grant to declare `raw-response`, checked at build.

## Bounds

- A 32MB size gate before the parser.
- A page bound defaulting to 20 and ceilinged at 200.
- A parse that runs off the request goroutine, so a wedged document returns a
  stated timeout rather than holding the handler.

Malformed and encrypted documents are clean tool errors, and a parser panic is
recovered into one. The coverage block gains `pages: {shown, total}`, so a
bounded read cannot be mistaken for the whole document.

Text only. Table extraction and OCR are separate decisions.

## Why in-process

A sidecar would put a second container, a second image to patch, and a network
hop between the runtime and a parse it already has the bytes for. The bounds
above are what make in-process safe: the size gate gets ahead of the parser,
the page bound caps the work, and the off-goroutine parse means a hostile or
broken document cannot hold a handler open.

See also: [guardfile-controls.md](guardfile-controls.md), [serve.md](serve.md).
