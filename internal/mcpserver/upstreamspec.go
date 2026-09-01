package mcpserver

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	kdl "github.com/calico32/kdl-go"
)

// upstreamTransport is the one transport the proxy speaks, so the node is a
// statement rather than a choice. See docs/upstream.md.
const upstreamTransport = "streamable-http"

// AnnotationCoverage is the `annotation-coverage` marker: whether the upstream
// declares `readOnlyHint` on every tool, some, or none. It is the one fact
// that decides whether a guardfile can safely allow anything, and no directory
// publishes it (mcp-beaver#119).
type AnnotationCoverage struct {
	// Kind is declared, partial, or undeclared.
	Kind string
	// Annotated counts tools carrying a readOnlyHint either way.
	Annotated int
	// Silent counts tools declaring nothing.
	Silent int
}

// UpstreamSpec is a `wrap mcp upstream` guardfile, parsed offline. It carries
// policy and never schemas: the proxy snapshots the upstream contracts at
// connect time and fails closed on drift, so restating them here would rot.
type UpstreamSpec struct {
	// Name is the wrap argument, the registry name by convention.
	Name string
	// URL is the streamable-HTTP endpoint.
	URL string
	// Tools is the allowlist in stated order. It may be empty: a guardfile
	// that exposes nothing is a valid statement about an upstream.
	Tools []string
	// Coverage is nil when the guardfile states no marker.
	Coverage *AnnotationCoverage
	// Headers is the credential an `auth header-token` node presents.
	Headers []UpstreamHeader
	// Providers is the value registry the headers resolve through.
	Providers ProviderSet
	// Instructions is the sibling `instructions` text, empty when absent.
	Instructions string
}

// Options is the proxy configuration this guardfile stands for.
func (u *UpstreamSpec) Options() ProxyOptions {
	return ProxyOptions{Headers: u.Headers, Providers: u.Providers, Instructions: u.Instructions}
}

// IsUpstreamSpec reports whether the top-level `wrap` opens `mcp upstream`
// rather than umbra's `ward mcp`. The two kinds are told apart here, before
// either parser runs, so the REST grammar never sees a proxy body.
func IsUpstreamSpec(src []byte) (bool, error) {
	wrap, err := wrapNode(src)
	if err != nil {
		return false, err
	}
	args := wrap.Arguments()
	return len(args) >= 2 && args[0].String() == "mcp" && args[1].String() == "upstream", nil
}

func wrapNode(src []byte) (*kdl.Node, error) {
	doc, err := parseInlineDoc(src, "wrap")
	if err != nil {
		return nil, err
	}
	wrap := doc.GetNode("wrap")
	if wrap == nil {
		return nil, fmt.Errorf("mcp-beaver: missing top-level `wrap` node")
	}
	return wrap, nil
}

// ParseUpstreamSpec reads a `wrap mcp upstream` guardfile:
//
//	wrap mcp upstream "ac.tandem/docs-mcp" {
//	    url "https://tandem.ac/mcp"
//	    transport streamable-http
//	    annotation-coverage partial annotated=7 silent=6
//	    auth header-token { header "Authorization"; prefix "Bearer "; value env "TOKEN" }
//	    can "search_docs"
//	}
//
// Every body node maps onto a `serve-upstream` flag that already exists, so
// this is expressibility rather than a new runtime capability. Beside the
// wrap, `instructions` and `oauth2-client` are accepted. Every other sibling
// fails closed until it is projected onto the proxy surface on purpose.
func ParseUpstreamSpec(specPath string, src []byte) (*UpstreamSpec, error) {
	wrap, err := wrapNode(src)
	if err != nil {
		return nil, err
	}
	args := wrap.Arguments()
	if len(args) != 3 || args[0].String() != "mcp" || args[1].String() != "upstream" {
		return nil, fmt.Errorf("mcp-beaver: `wrap mcp upstream` wants exactly one name argument, as `wrap mcp upstream \"<registry-name>\"`")
	}
	spec := &UpstreamSpec{Name: args[2].String()}
	if spec.Name == "" {
		return nil, fmt.Errorf("mcp-beaver: `wrap mcp upstream` name must be non-empty")
	}
	if len(wrap.Properties()) > 0 {
		return nil, fmt.Errorf("mcp-beaver: `wrap mcp upstream` takes no properties")
	}
	dir := "."
	if specPath != "" {
		dir = filepath.Dir(specPath)
	}
	sources := singleSource(src, dir)
	clients, err := parseOAuth2Clients(sources)
	if err != nil {
		return nil, err
	}
	spec.Providers, err = NewProviderSet(clients, nil)
	if err != nil {
		return nil, err
	}
	if err := parseUpstreamBody(wrap, spec); err != nil {
		return nil, err
	}
	if spec.URL == "" {
		return nil, fmt.Errorf("mcp-beaver: `wrap mcp upstream` needs a `url` node naming the streamable-HTTP endpoint")
	}
	if err := ValidateUpstreamHeaders(spec.Headers); err != nil {
		return nil, err
	}
	if err := rejectUnprojectedSiblings(sources); err != nil {
		return nil, err
	}
	spec.Instructions, err = parseInstructions(sources)
	if err != nil {
		return nil, err
	}
	return spec, nil
}

func parseUpstreamBody(wrap *kdl.Node, spec *UpstreamSpec) error {
	seen := map[string]bool{}
	once := func(n *kdl.Node) error {
		if seen[n.Name()] {
			return fmt.Errorf("mcp-beaver: duplicate `%s` in `wrap mcp upstream` (fail-closed)", n.Name())
		}
		seen[n.Name()] = true
		return nil
	}
	tools := map[string]bool{}
	for _, n := range wrap.Children().Nodes {
		switch n.Name() {
		case "url":
			if err := once(n); err != nil {
				return err
			}
			raw, err := oneStringArg(n, "url")
			if err != nil {
				return err
			}
			if err := validateUpstreamURL(raw); err != nil {
				return err
			}
			spec.URL = raw
		case "transport":
			if err := once(n); err != nil {
				return err
			}
			got, err := oneStringArg(n, "transport")
			if err != nil {
				return err
			}
			if got != upstreamTransport {
				return fmt.Errorf("mcp-beaver: `transport %s` is not served; the proxy speaks %s only", got, upstreamTransport)
			}
		case "annotation-coverage":
			if err := once(n); err != nil {
				return err
			}
			coverage, err := parseAnnotationCoverage(n)
			if err != nil {
				return err
			}
			spec.Coverage = coverage
		case "auth":
			if err := once(n); err != nil {
				return err
			}
			header, err := parseUpstreamAuth(n, spec.Providers)
			if err != nil {
				return err
			}
			spec.Headers = append(spec.Headers, header)
		case "can":
			tool, err := oneStringArg(n, "can")
			if err != nil {
				return err
			}
			if len(n.Properties()) > 0 || len(n.Children().Nodes) > 0 {
				return fmt.Errorf("mcp-beaver: `can %q` takes no properties or children in `wrap mcp upstream`: the contract stays upstream", tool)
			}
			if tools[tool] {
				return fmt.Errorf("mcp-beaver: duplicate `can %q` in `wrap mcp upstream`", tool)
			}
			tools[tool] = true
			spec.Tools = append(spec.Tools, tool)
		default:
			return fmt.Errorf("mcp-beaver: `%s` is not part of `wrap mcp upstream`; the body takes url, transport, annotation-coverage, auth, and can (fail-closed)", n.Name())
		}
	}
	return nil
}

func validateUpstreamURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("mcp-beaver: `url %q` does not parse: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("mcp-beaver: `url %q` must be an absolute http or https URL", raw)
	}
	return nil
}

// parseAnnotationCoverage reads the marker and checks it against itself, so a
// hand edit cannot state `declared` beside a non-zero silent count.
func parseAnnotationCoverage(n *kdl.Node) (*AnnotationCoverage, error) {
	kind, err := oneStringArg(n, "annotation-coverage")
	if err != nil {
		return nil, err
	}
	if len(n.Children().Nodes) > 0 {
		return nil, fmt.Errorf("mcp-beaver: `annotation-coverage` takes no children")
	}
	out := &AnnotationCoverage{Kind: kind}
	have := map[string]bool{}
	for key, value := range n.Properties() {
		count, err := countProp(value, "annotation-coverage", key)
		if err != nil {
			return nil, err
		}
		switch key {
		case "annotated":
			out.Annotated = count
		case "silent":
			out.Silent = count
		default:
			return nil, fmt.Errorf("mcp-beaver: unknown `annotation-coverage` property %q (want annotated, silent)", key)
		}
		have[key] = true
	}
	if !have["annotated"] || !have["silent"] {
		return nil, fmt.Errorf("mcp-beaver: `annotation-coverage` wants both annotated= and silent= counts")
	}
	if err := out.validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func (c AnnotationCoverage) validate() error {
	switch c.Kind {
	case "declared":
		if c.Silent != 0 || c.Annotated == 0 {
			return fmt.Errorf("mcp-beaver: `annotation-coverage declared` wants silent=0 and annotated>0, got annotated=%d silent=%d", c.Annotated, c.Silent)
		}
	case "undeclared":
		if c.Annotated != 0 {
			return fmt.Errorf("mcp-beaver: `annotation-coverage undeclared` wants annotated=0, got annotated=%d", c.Annotated)
		}
	case "partial":
		if c.Annotated == 0 || c.Silent == 0 {
			return fmt.Errorf("mcp-beaver: `annotation-coverage partial` wants both counts above zero, got annotated=%d silent=%d", c.Annotated, c.Silent)
		}
	default:
		return fmt.Errorf("mcp-beaver: unknown `annotation-coverage` kind %q (want declared, partial, undeclared)", c.Kind)
	}
	return nil
}

func countProp(value kdl.Value, node, key string) (int, error) {
	if value.Kind() != kdl.Int {
		return 0, fmt.Errorf("mcp-beaver: `%s` property %q wants a whole number, got %s", node, key, value.String())
	}
	count := value.Int()
	if count < 0 {
		return 0, fmt.Errorf("mcp-beaver: `%s` property %q must not be negative", node, key)
	}
	return int(count), nil
}

// parseUpstreamAuth reads spec mode's `auth header-token` shape and lifts it
// onto the header template `serve-upstream --upstream-header` already takes,
// so a credential reads the same in both modes and resolves through the same
// registry.
func parseUpstreamAuth(n *kdl.Node, providers ProviderSet) (UpstreamHeader, error) {
	scheme, err := oneStringArg(n, "auth")
	if err != nil {
		return UpstreamHeader{}, err
	}
	if scheme != "header-token" {
		return UpstreamHeader{}, fmt.Errorf("mcp-beaver: `auth %s` is not served in `wrap mcp upstream`; header-token is the one scheme", scheme)
	}
	var header, prefix, provider, address string
	seen := map[string]bool{}
	for _, child := range n.Children().Nodes {
		if seen[child.Name()] {
			return UpstreamHeader{}, fmt.Errorf("mcp-beaver: duplicate `auth` child %q", child.Name())
		}
		seen[child.Name()] = true
		switch child.Name() {
		case "header":
			header, err = oneStringArg(child, "header")
		case "prefix":
			prefix, err = oneStringArg(child, "prefix")
		case "value":
			if len(child.Arguments()) != 2 {
				return UpstreamHeader{}, fmt.Errorf("mcp-beaver: `auth` child `value` wants `<provider> \"<address>\"`, got %d arguments", len(child.Arguments()))
			}
			provider, address = child.Arg(0).String(), child.Arg(1).String()
		default:
			return UpstreamHeader{}, fmt.Errorf("mcp-beaver: unknown `auth` child %q (want header, prefix, value)", child.Name())
		}
		if err != nil {
			return UpstreamHeader{}, err
		}
	}
	if header == "" || provider == "" || address == "" {
		return UpstreamHeader{}, fmt.Errorf("mcp-beaver: `auth header-token` wants `header` and `value <provider> \"<address>\"` children")
	}
	if strings.ContainsAny(prefix+provider+address, "{}") {
		return UpstreamHeader{}, fmt.Errorf("mcp-beaver: `auth` prefix and value may not contain braces")
	}
	return ParseUpstreamHeader(fmt.Sprintf("%s=%s{%s:%s}", header, prefix, provider, address), providers)
}

// upstreamSiblings names the sibling nodes projected onto a proxy surface.
// The rest are parsed for a REST guardfile only, and a proxy that silently
// ignored a `withhold` or `confirm` would serve WIDER than the file reads.
var upstreamSiblings = map[string]bool{
	"instructions":  true,
	"oauth2-client": true,
}

func rejectUnprojectedSiblings(sources []guardSource) error {
	nodes, err := parseInlineNodes(sources, "wrap mcp upstream")
	if err != nil {
		return err
	}
	for _, sn := range nodes {
		if !upstreamSiblings[sn.node.Name()] {
			return fmt.Errorf("mcp-beaver: `%s` is not yet projected beside `wrap mcp upstream`; instructions and oauth2-client are (fail-closed)", sn.node.Name())
		}
	}
	return nil
}
