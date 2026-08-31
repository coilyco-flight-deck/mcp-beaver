package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	kdl "github.com/calico32/kdl-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// appMIMEType is what a host matches on to decide a resource is a widget
// rather than reference text. Measured on the wire from two published
// reference servers (`server-basic-vanillajs`, `server-map`), and stated as
// SHOULD in specification/2026-01-26/apps.mdx.
const appMIMEType = "text/html;profile=mcp-app"

// appURIScheme is the scheme a host keys the widget lookup on. A widget served
// at any other scheme is fetched by nobody, so this fails closed rather than
// serving a resource that reads correct and renders never.
const appURIScheme = "ui://"

// appMaxBytes bounds one widget. The four published examples measure 317 KB to
// 545 KB, roughly 98% of each being one inline script block, so the ceiling is
// an order of magnitude above a real bundle. It exists because the body is
// read whole into memory at startup and a mistyped path should fail with a
// number rather than with the pod's memory limit.
const appMaxBytes = 10 << 20

// uiMetaKey is the nested `_meta` object both the tool and the resource carry.
const uiMetaKey = "ui"

// uiFlatResourceURIKey is the flattened spelling the reference servers emit
// BESIDE the nested object, not instead of it. Both were measured on
// tools/list from `server-basic-vanillajs` and `server-map`, so a host reading
// either era finds the link. Only resourceUri is flattened upstream, and
// `visibility` is not, so this mirrors that rather than generalising it.
const uiFlatResourceURIKey = "ui/resourceUri"

// mcpApp is one `app` node: an MCP App widget served at a `ui://` URI, plus
// the projected tools whose `_meta.ui.resourceUri` points at it.
//
// Beaver is the producer end of MCP Apps and not the host: it declares the
// widget and the link, and the rendering, the sandbox, and the postMessage
// bridge are all the host's. What beaver adds over the reference servers is
// that the tools a widget can reach are the guardfile's `can` grants and
// nothing else, so the widget's blast radius is the same audited file as the
// agent's.
type mcpApp struct {
	resource mcp.Resource
	body     string
	uiMeta   map[string]any
	tools    []appTool
	// inherited marks an app a BASE tier declared, so a tool link the child
	// narrowed away is vacated rather than a mistake. See compose.go.
	inherited bool
}

// appTool is one `tool` child: the projected tool name that carries the link,
// and the visibility it advertises.
type appTool struct {
	name       string
	visibility []string
}

// appVisibility is the closed set from McpUiToolVisibility. "model" means the
// agent sees and calls the tool, "app" means the widget may call it. Stating
// neither is not the same as stating both: an omitted visibility leaves the
// spec default of ["model","app"] in the host's hands, which is what an author
// who never thought about it should get.
var appVisibility = map[string]bool{"model": true, "app": true}

// appPermissions is the closed set from McpUiResourcePermissions. Each maps to
// one Permission Policy feature the host MAY put on the iframe.
var appPermissions = map[string]string{
	"camera":         "camera",
	"microphone":     "microphone",
	"geolocation":    "geolocation",
	"clipboardWrite": "clipboardWrite",
}

// appCSPDirectives maps the guardfile's `csp` children onto McpUiResourceCsp.
// Every omitted list is 'none' at the host, so an app declaring no csp reaches
// no origin at all - deny-by-absence, arriving here from the browser's own
// default rather than from anything this runtime enforces.
var appCSPDirectives = map[string]string{
	"connect":  "connectDomains",
	"resource": "resourceDomains",
	"frame":    "frameDomains",
	"base-uri": "baseUriDomains",
}

// parseApps reads top-level `app` nodes, siblings of `wrap` in the same
// position as `resource`:
//
//	app "cesium-map" uri="ui://cesium-map/mcp-app.html" file="widgets/map.html" prefers-border=#true {
//	    description "An interactive globe"
//	    tool "show_map"
//	    tool "geocode" visibility="app"
//	    csp {
//	        connect "https://*.cesium.com"
//	        resource "https://*.cesium.com"
//	    }
//	    permission "clipboardWrite"
//	}
//
// The body comes from a FILE rather than `text` children, which is the one
// place this node departs from `resource`. That node's inline-only rule is
// about egress: a resource proxying an upstream read would be a second,
// unguarded path beside the grants. Reading a local file once at startup
// reaches no network and adds no such path, and a widget is 300 KB of bundled
// script, which no KDL string literal should ever hold.
//
// The path resolves against the guardfile's own directory, so a spec and its
// widgets move together. A deployment mounting the spec now mounts the widget
// beside it.
func parseApps(sources []guardSource) ([]mcpApp, error) {
	nodes, err := parseInlineNodes(sources, "apps")
	if err != nil {
		return nil, err
	}
	var out []mcpApp
	seenURI := map[string]bool{}
	seenName := map[string]bool{}
	for _, sn := range nodes {
		n := sn.node
		if n.Name() != "app" {
			continue
		}
		// The declaring guardfile's directory, not the mounted one: an
		// inherited `app` names a widget beside the file that declared it.
		app, err := parseApp(n, sn.dir)
		if err != nil {
			return nil, err
		}
		app.inherited = sn.index < len(sources)-1
		if seenName[app.resource.Name] {
			return nil, fmt.Errorf("mcp-beaver: duplicate `app` name %q", app.resource.Name)
		}
		seenName[app.resource.Name] = true
		if seenURI[app.resource.URI] {
			return nil, fmt.Errorf("mcp-beaver: duplicate `app` uri %q", app.resource.URI)
		}
		seenURI[app.resource.URI] = true
		out = append(out, app)
	}
	return out, nil
}

func parseApp(n *kdl.Node, specDir string) (mcpApp, error) {
	name, err := oneStringArg(n, "app")
	if err != nil {
		return mcpApp{}, err
	}
	app := mcpApp{resource: mcp.Resource{Name: name, MIMEType: appMIMEType}, uiMeta: map[string]any{}}
	var file string
	for key, value := range n.Properties() {
		switch key {
		case "uri":
			app.resource.URI = value.String()
		case "file":
			file = value.String()
		case "title":
			app.resource.Title = value.String()
		case "domain":
			if value.String() == "" {
				return mcpApp{}, fmt.Errorf("mcp-beaver: `app` %q domain must be non-empty", name)
			}
			app.uiMeta["domain"] = value.String()
		case "prefers-border":
			border, err := boolProp(value, "app", key)
			if err != nil {
				return mcpApp{}, err
			}
			app.uiMeta["prefersBorder"] = border
		default:
			return mcpApp{}, fmt.Errorf("mcp-beaver: unknown `app` property %q (want uri | file | title | domain | prefers-border; fail-closed)", key)
		}
	}
	if !strings.HasPrefix(app.resource.URI, appURIScheme) {
		return mcpApp{}, fmt.Errorf("mcp-beaver: `app` %q needs a uri under %s, got %q", name, appURIScheme, app.resource.URI)
	}
	if file == "" {
		return mcpApp{}, fmt.Errorf("mcp-beaver: `app` %q needs a non-empty file", name)
	}
	if app.body, err = readAppBody(file, specDir, name); err != nil {
		return mcpApp{}, err
	}
	if err := parseAppChildren(n, &app); err != nil {
		return mcpApp{}, err
	}
	// A widget no tool points at is fetched by no host: `_meta.ui.resourceUri`
	// on a tool is the only thing that reaches one. Serving it anyway would
	// lint clean and render never, which is the same silent failure the
	// `audience` warning exists for, one step further along.
	if len(app.tools) == 0 {
		return mcpApp{}, fmt.Errorf("mcp-beaver: `app` %q names no `tool`, so no host would ever fetch it", name)
	}
	return app, nil
}

func parseAppChildren(n *kdl.Node, app *mcpApp) error {
	name := app.resource.Name
	csp := map[string]any{}
	permissions := map[string]any{}
	seenTool := map[string]bool{}
	for _, child := range n.Children().Nodes {
		switch child.Name() {
		case "description":
			if len(child.Arguments()) != 1 {
				return fmt.Errorf("mcp-beaver: `app` %q child `description` wants exactly one argument", name)
			}
			app.resource.Description = child.Arg(0).String()
		case "tool":
			tool, err := parseAppTool(child, name)
			if err != nil {
				return err
			}
			if seenTool[tool.name] {
				return fmt.Errorf("mcp-beaver: `app` %q repeats tool %q", name, tool.name)
			}
			seenTool[tool.name] = true
			app.tools = append(app.tools, tool)
		case "csp":
			if err := parseAppCSP(child, name, csp); err != nil {
				return err
			}
		case "permission":
			if err := parseAppPermissions(child, name, permissions); err != nil {
				return err
			}
		default:
			return fmt.Errorf("mcp-beaver: unknown `app` child %q on %q (want description | tool | csp | permission; fail-closed)", child.Name(), name)
		}
	}
	if len(csp) > 0 {
		app.uiMeta["csp"] = csp
	}
	if len(permissions) > 0 {
		app.uiMeta["permissions"] = permissions
	}
	return nil
}

func parseAppTool(n *kdl.Node, appName string) (appTool, error) {
	name, err := oneStringArg(n, "tool")
	if err != nil {
		return appTool{}, err
	}
	tool := appTool{name: name}
	for key, value := range n.Properties() {
		if key != "visibility" {
			return appTool{}, fmt.Errorf("mcp-beaver: unknown `app` %q tool property %q (want visibility; fail-closed)", appName, key)
		}
		seen := map[string]bool{}
		for _, role := range strings.Fields(value.String()) {
			if !appVisibility[role] {
				return appTool{}, fmt.Errorf("mcp-beaver: `app` %q tool %q visibility %q is not model or app (fail-closed)", appName, name, role)
			}
			if seen[role] {
				return appTool{}, fmt.Errorf("mcp-beaver: `app` %q tool %q repeats visibility %q", appName, name, role)
			}
			seen[role] = true
			tool.visibility = append(tool.visibility, role)
		}
		if len(tool.visibility) == 0 {
			return appTool{}, fmt.Errorf("mcp-beaver: `app` %q tool %q visibility names no role", appName, name)
		}
	}
	return tool, nil
}

func parseAppCSP(n *kdl.Node, appName string, csp map[string]any) error {
	for _, child := range n.Children().Nodes {
		field, known := appCSPDirectives[child.Name()]
		if !known {
			return fmt.Errorf("mcp-beaver: unknown `app` %q csp child %q (want connect | resource | frame | base-uri; fail-closed)", appName, child.Name())
		}
		if len(child.Arguments()) == 0 {
			return fmt.Errorf("mcp-beaver: `app` %q csp %q wants at least one origin", appName, child.Name())
		}
		origins, _ := csp[field].([]string)
		for i := range child.Arguments() {
			origin := child.Arg(i).String()
			if origin == "" {
				return fmt.Errorf("mcp-beaver: `app` %q csp %q origin must be non-empty", appName, child.Name())
			}
			origins = append(origins, origin)
		}
		csp[field] = origins
	}
	return nil
}

func parseAppPermissions(n *kdl.Node, appName string, permissions map[string]any) error {
	if len(n.Arguments()) == 0 {
		return fmt.Errorf("mcp-beaver: `app` %q `permission` wants at least one capability", appName)
	}
	for i := range n.Arguments() {
		want := n.Arg(i).String()
		field, known := appPermissions[want]
		if !known {
			return fmt.Errorf("mcp-beaver: `app` %q permission %q is not camera, microphone, geolocation, or clipboardWrite (fail-closed)", appName, want)
		}
		// The spec's shape is an empty object per requested feature, not a
		// boolean: a permission is present or absent, and there is no
		// "requested false" to express.
		permissions[field] = map[string]any{}
	}
	return nil
}

// readAppBody reads the widget bundle beside the guardfile. An absolute path
// is refused: a guardfile is mounted into a container that has no such path,
// and the failure would arrive at startup on the deployment rather than at
// lint on the author's machine.
func readAppBody(file, specDir, name string) (string, error) {
	if filepath.IsAbs(file) {
		return "", fmt.Errorf("mcp-beaver: `app` %q file %q must be relative to the guardfile", name, file)
	}
	path := filepath.Join(specDir, file)
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("mcp-beaver: `app` %q file %q: %w", name, file, err)
	}
	if info.Size() > appMaxBytes {
		return "", fmt.Errorf("mcp-beaver: `app` %q file %q is %d bytes, over the %d-byte bound", name, file, info.Size(), appMaxBytes)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("mcp-beaver: `app` %q file %q: %w", name, file, err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("mcp-beaver: `app` %q file %q is empty", name, file)
	}
	return string(body), nil
}

// validateApps rejects an `app` naming a tool the spec does not mint, and two
// apps claiming the same tool. Both are the `confirm` failure in a different
// costume: the author believes a widget renders for a tool, the link is
// attached to nothing or to the wrong one of two, and the server serves
// cleanly with the interface missing.
func validateApps(apps []mcpApp, tools []*mcp.Tool, resources []inlineResource) error {
	served := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if tool != nil {
			served[tool.Name] = true
		}
	}
	uris := make(map[string]string, len(resources))
	for _, res := range resources {
		uris[res.tool.URI] = "resource"
	}
	claimed := map[string]string{}
	for _, app := range apps {
		if owner, taken := uris[app.resource.URI]; taken {
			return fmt.Errorf("mcp-beaver: `app` %q uri %q is already served by a `%s`", app.resource.Name, app.resource.URI, owner)
		}
		uris[app.resource.URI] = "app"
		for _, tool := range app.tools {
			if !served[tool.name] {
				return fmt.Errorf("mcp-beaver: `app` %q names tool %q, which this spec does not mint", app.resource.Name, tool.name)
			}
			if owner, taken := claimed[tool.name]; taken {
				return fmt.Errorf("mcp-beaver: tool %q is claimed by both `app` %q and `app` %q, and a tool carries one widget", tool.name, owner, app.resource.Name)
			}
			claimed[tool.name] = app.resource.Name
		}
	}
	return nil
}

// appToolMeta indexes the projected tool name onto the `_meta` a tool carries,
// so both projections of the served surface - the list `lint` and telemetry
// read, and the specs the register loop builds - attach the same link.
func appToolMeta(apps []mcpApp) map[string]map[string]any {
	if len(apps) == 0 {
		return nil
	}
	out := map[string]map[string]any{}
	for _, app := range apps {
		for _, tool := range app.tools {
			ui := map[string]any{"resourceUri": app.resource.URI}
			if len(tool.visibility) > 0 {
				ui["visibility"] = tool.visibility
			}
			out[tool.name] = map[string]any{
				uiMetaKey:            ui,
				uiFlatResourceURIKey: app.resource.URI,
			}
		}
	}
	return out
}

// applyAppMeta attaches the widget link to one projected tool, merging into
// whatever `_meta` the tool already carries rather than replacing it: a
// `withhold` stub marks itself in the same map.
func applyAppMeta(meta map[string]map[string]any, tool *mcp.Tool) {
	if tool == nil || meta == nil {
		return
	}
	add, linked := meta[tool.Name]
	if !linked {
		return
	}
	merged := map[string]any{}
	for key, value := range tool.GetMeta() {
		merged[key] = value
	}
	for key, value := range add {
		merged[key] = value
	}
	tool.SetMeta(merged)
}

// registerApps serves each widget on `resources/read`. The `_meta.ui` block
// rides on the CONTENT item rather than on the resource entry, which is where
// the spec says a host reads it and where the published `server-map` was
// measured putting its csp.
//
// App resources are deliberately kept out of s.resources: that list feeds the
// `audience` lint, and a widget is fetched by a host following a tool link
// rather than pulled into a model's context, so an audience annotation on one
// would be noise the author has no way to satisfy correctly.
func (s *Server) registerApps(apps []mcpApp) {
	for _, a := range apps {
		app := a
		s.apps = append(s.apps, app.resource)
		for _, tool := range app.tools {
			if s.appTools == nil {
				s.appTools = map[string]string{}
			}
			s.appTools[tool.name] = app.resource.URI
		}
		s.sdk.AddResource(&app.resource, func(_ context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			contents := &mcp.ResourceContents{
				URI:      app.resource.URI,
				MIMEType: app.resource.MIMEType,
				Text:     app.body,
			}
			if len(app.uiMeta) > 0 {
				contents.Meta = mcp.Meta{uiMetaKey: app.uiMeta}
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{contents}}, nil
		})
	}
}

// AppTools maps each projected tool onto the widget uri it carries, so `lint`
// reports the MCP App surface offline rather than leaving it to a live
// handshake against a running pod.
func (s *Server) AppTools() map[string]string {
	out := make(map[string]string, len(s.appTools))
	for name, uri := range s.appTools {
		out[name] = uri
	}
	return out
}

// dropVacantAppLinks removes a link an inherited `app` states to a tool the
// child narrowed away, and the whole app once no link is left. Same reasoning
// as dropVacantControls: a widget with no tool pointing at it is fetched by
// nobody, and refusing would strand a base tier that gated a tool this tier
// correctly removed.
func dropVacantAppLinks(apps []mcpApp, minted map[string]bool) ([]mcpApp, []string) {
	var kept []mcpApp
	var vacated []string
	for _, app := range apps {
		if !app.inherited {
			kept = append(kept, app)
			continue
		}
		var links []appTool
		for _, tool := range app.tools {
			if minted[tool.name] {
				links = append(links, tool)
				continue
			}
			vacated = append(vacated, fmt.Sprintf("app %q link to %s", app.resource.Name, tool.name))
		}
		if len(links) == 0 {
			continue
		}
		app.tools = links
		kept = append(kept, app)
	}
	sort.Strings(vacated)
	return kept, vacated
}
