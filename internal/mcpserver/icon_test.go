package mcpserver

import (
	"strings"
	"testing"
)

// minimal wrap body so the icon-bearing documents stay valid guardfiles - the
// boolean shorthand exercises the normalize shim the whole-document parse
// needs.
const iconTestWrap = `wrap ward mcp test {
    base-url "https://upstream.example"
    can get thing {
        path "/things/{id}"
        body {
            field "title" type="string" required=true
        }
    }
}`

func TestParseIconsAbsent(t *testing.T) {
	icons, err := parseIcons([]byte(iconTestWrap))
	if err != nil {
		t.Fatalf("parseIcons: %v", err)
	}
	if icons != nil {
		t.Fatalf("want no icons, got %d", len(icons))
	}
}

func TestParseIconsDataURI(t *testing.T) {
	doc := `icon "data:image/png;base64,aGVsbG8="` + "\n" + iconTestWrap
	icons, err := parseIcons([]byte(doc))
	if err != nil {
		t.Fatalf("parseIcons: %v", err)
	}
	if len(icons) != 1 {
		t.Fatalf("want 1 icon, got %d", len(icons))
	}
	if got := icons[0].Source; !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("src not preserved: %q", got)
	}
	if icons[0].MIMEType != "image/png" {
		t.Fatalf("mime not inferred from data URI: %q", icons[0].MIMEType)
	}
}

func TestParseIconsExplicitProps(t *testing.T) {
	doc := `icon "https://static.example/icon.svg" mime="image/svg+xml" sizes="any"` + "\n" + iconTestWrap
	icons, err := parseIcons([]byte(doc))
	if err != nil {
		t.Fatalf("parseIcons: %v", err)
	}
	if len(icons) != 1 {
		t.Fatalf("want 1 icon, got %d", len(icons))
	}
	if icons[0].MIMEType != "image/svg+xml" {
		t.Fatalf("mime: %q", icons[0].MIMEType)
	}
	if len(icons[0].Sizes) != 1 || icons[0].Sizes[0] != "any" {
		t.Fatalf("sizes: %v", icons[0].Sizes)
	}
}

func TestParseIconsMultiple(t *testing.T) {
	doc := `icon "data:image/png;base64,aGVsbG8=" sizes="256x256"
icon "data:image/png;base64,d29ybGQ=" sizes="48x48"
` + iconTestWrap
	icons, err := parseIcons([]byte(doc))
	if err != nil {
		t.Fatalf("parseIcons: %v", err)
	}
	if len(icons) != 2 {
		t.Fatalf("want 2 icons, got %d", len(icons))
	}
}

func TestParseIconsRejectsMissingSrc(t *testing.T) {
	doc := "icon\n" + iconTestWrap
	if _, err := parseIcons([]byte(doc)); err == nil {
		t.Fatal("want error for icon with no src argument")
	}
}

func TestParseIconsRejectsUnknownProperty(t *testing.T) {
	doc := `icon "data:image/png;base64,aGVsbG8=" theme="dark"` + "\n" + iconTestWrap
	if _, err := parseIcons([]byte(doc)); err == nil {
		t.Fatal("want fail-closed error for unknown icon property")
	}
}
