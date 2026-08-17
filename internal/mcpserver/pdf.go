package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"forgejo.coilysiren.me/coilyco-flight-deck/umbra/http/opcore"
	pdf "github.com/dslipak/pdf"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxPDFBytes bounds the document this runtime will open at all. Parsing is
// the one place a granted upstream hands this process an arbitrarily large,
// arbitrarily structured input, so the size gate comes before the parser
// rather than from it.
const maxPDFBytes = 32 << 20

// defaultExtractPages is the page bound when a guardfile states none. PDFs run
// to hundreds of pages routinely, and returning a whole document's text would
// blow any agent's context - so the bound is the default and the guardfile
// raises it, never the other way round.
const defaultExtractPages = 20

// maxExtractPages ceilings what a guardfile may raise the bound to. Past this
// the answer is a different tool rather than a bigger number.
const maxExtractPages = 200

// pdfExtract is one grant's declared PDF-to-text projection.
type pdfExtract struct {
	maxPages int
}

// extractConfig maps a projected tool name to its extraction.
type extractConfig map[string]*pdfExtract

// parseExtracts reads top-level `extract` nodes, siblings of `wrap`:
//
//	extract "get_report" as="pdf-text" max-pages="20"
//
// A large amount of authoritative reference material - government statistics,
// standards bodies, regulatory filings, equipment documentation - is published
// only as PDF, with no JSON API and often no HTML equivalent. A grant that
// reaches one returns bytes an agent cannot read, so those sources are
// invisible to the whole fleet (mcp-beaver#60).
//
// The projection lives here rather than in the guardfile grammar because
// turning an upstream response into tool content is this runtime's half of the
// boundary: umbra owns guarded execution, mcp-beaver owns projection.
//
// `as` names the extraction and takes only "pdf-text" today. Table extraction
// and OCR are three very different amounts of work with three very different
// dependency footprints, and text alone covers most of the stated need - so
// the property exists to make the next one additive rather than to suggest it
// already exists.
func parseExtracts(src []byte) (extractConfig, error) {
	doc, err := parseInlineDoc(src, "extract")
	if err != nil {
		return nil, err
	}
	out := extractConfig{}
	for _, n := range doc.Nodes {
		if n.Name() != "extract" {
			continue
		}
		tool, err := oneStringArg(n, "extract")
		if err != nil {
			return nil, err
		}
		if _, dup := out[tool]; dup {
			return nil, fmt.Errorf("mcp-beaver: duplicate `extract` for tool %q", tool)
		}
		kind := ""
		pages := defaultExtractPages
		for key, value := range n.Properties() {
			switch key {
			case "as":
				kind = value.String()
			case "max-pages":
				pages, err = strconv.Atoi(value.String())
				if err != nil {
					return nil, fmt.Errorf("mcp-beaver: `extract` %q max-pages %q must be a whole number", tool, value.String())
				}
			default:
				return nil, fmt.Errorf("mcp-beaver: unknown `extract` property %q (want as | max-pages; fail-closed)", key)
			}
		}
		if kind != "pdf-text" {
			return nil, fmt.Errorf("mcp-beaver: `extract` %q needs `as=\"pdf-text\"`, got %q", tool, kind)
		}
		if pages < 1 || pages > maxExtractPages {
			return nil, fmt.Errorf("mcp-beaver: `extract` %q max-pages must be between 1 and %d, got %d", tool, maxExtractPages, pages)
		}
		out[tool] = &pdfExtract{maxPages: pages}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// validateExtracts rejects an `extract` that could never run. Both cases are
// build errors because an author who believes a PDF is being read and one who
// believes it is not should not both be able to run the same spec.
func validateExtracts(cfg extractConfig, descs []opcore.Descriptor) error {
	if cfg == nil {
		return nil
	}
	byTool := make(map[string]opcore.Descriptor, len(descs))
	for _, d := range descs {
		byTool[toolName(d)] = d
	}
	for tool := range cfg {
		desc, ok := byTool[tool]
		if !ok {
			return fmt.Errorf("mcp-beaver: `extract` names %q, which is not a grant-backed tool this spec serves", tool)
		}
		// Without `raw-response` opcore decodes the body as JSON and the call
		// fails on the first byte of `%PDF`, long before anything here runs.
		if !desc.RawResponse {
			return fmt.Errorf("mcp-beaver: `extract` names %q, whose grant does not declare `raw-response`: opcore would decode the PDF as JSON and fail the call before extraction", tool)
		}
	}
	return nil
}

// pdfToolSuccess replaces the PDF bytes with the text an agent can read, and
// reports pages shown of pages total in the coverage block so a bounded read
// cannot be mistaken for the whole document (mcp-beaver#68).
func pdfToolSuccess(ctx context.Context, resp opcore.Response, x *pdfExtract) *mcp.CallToolResult {
	if len(resp.Raw) > maxPDFBytes {
		return toolError(fmt.Errorf("mcp-beaver: PDF is %d bytes, over the %d-byte extraction ceiling", len(resp.Raw), maxPDFBytes))
	}
	text, shown, total, err := extractPDFText(ctx, resp.Raw, x.maxPages)
	if err != nil {
		return toolError(err)
	}
	// Bytes measures the extracted text rather than the source document: it is
	// what the consumer carries, and it is what its cap applies to.
	payload := toolPayload{
		Coverage: coverage{
			Truncated:  false,
			Bytes:      len(text),
			OverBudget: len(text) > consumerBudgetBytes,
			Pages:      &pageCoverage{Shown: shown, Total: total},
		},
		Result: text,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return toolError(fmt.Errorf("serialize tool result: %w", err))
	}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		StructuredContent: payload,
	}
}

// pdfOutcome is one parse attempt, carried off the parser goroutine.
type pdfOutcome struct {
	text         string
	shown, total int
	err          error
}

// extractPDFText reads up to maxPages pages and reports how many the document
// held.
//
// The parse runs on its own goroutine and the caller selects on ctx, so a
// document that wedges the parser returns a stated timeout rather than holding
// the request open - #49 recorded a 180s hang inside two healthy pods, and a
// slow parse is exactly that shape. The goroutine can outlive the call, which
// is the deliberate trade: an abandoned parse is bounded by maxPDFBytes and by
// the pod's memory limit, where a blocked handler is bounded by nothing.
//
// A panic inside the parser becomes an error. rsc.io/pdf is pure Go, so a
// malformed document cannot corrupt memory, but it can and does panic on
// structures it does not handle, and a served pod must not die of a document
// someone linked.
func extractPDFText(ctx context.Context, raw []byte, maxPages int) (text string, shown, total int, err error) {
	done := make(chan pdfOutcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- pdfOutcome{err: fmt.Errorf("mcp-beaver: PDF is malformed or unsupported: %v", r)}
			}
		}()
		done <- readPDFText(raw, maxPages)
	}()
	select {
	case <-ctx.Done():
		return "", 0, 0, fmt.Errorf("mcp-beaver: PDF extraction did not finish: %w", ctx.Err())
	case got := <-done:
		return got.text, got.shown, got.total, got.err
	}
}

// readPDFText walks pages in order, stopping at the page bound, and reports
// how many the document held so the caller can say shown-of-total.
//
// Fonts are cached across pages the way the fork's own whole-document reader
// does: a charmap is parsed per font, and a report reusing one font on forty
// pages would otherwise parse it forty times.
func readPDFText(raw []byte, maxPages int) (out pdfOutcome) {
	reader, err := pdf.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		// Encrypted documents land here too. Naming the document rather than
		// the library keeps the message actionable for whoever wrote the grant.
		out.err = fmt.Errorf("mcp-beaver: cannot read PDF: %w", err)
		return out
	}
	out.total = reader.NumPage()
	fonts := map[string]*pdf.Font{}
	var b strings.Builder
	for i := 1; i <= out.total && out.shown < maxPages; i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		for _, name := range page.Fonts() {
			if _, seen := fonts[name]; !seen {
				font := page.Font(name)
				fonts[name] = &font
			}
		}
		text, err := page.GetPlainText(fonts)
		if err != nil {
			out.err = fmt.Errorf("mcp-beaver: cannot read PDF page %d: %w", i, err)
			return out
		}
		out.shown++
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimSpace(text))
	}
	out.text = b.String()
	return out
}
