package pages

import (
	"strings"
	"testing"
)

func TestRenderMarkdownStructure(t *testing.T) {
	got := string(RenderMarkdown("# Title\n\nHello **world**,\nsecond line.\n\n- one\n- two\n\n1. first\n2. second\n\n### Small"))
	want := "<h2>Title</h2>\n<p>Hello <strong>world</strong>, second line.</p>\n<ul>\n<li>one</li>\n<li>two</li>\n</ul>\n" +
		"<ol>\n<li>first</li>\n<li>second</li>\n</ol>\n<h3>Small</h3>\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderMarkdownLinks(t *testing.T) {
	got := string(RenderMarkdown("See [terms](/terms), [site](https://example.co.ke) and [mail](mailto:a@b.ke)."))
	for _, want := range []string{`<a href="/terms">terms</a>`, `<a href="https://example.co.ke" rel="noopener noreferrer">site</a>`, `<a href="mailto:a@b.ke">mail</a>`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}

func TestRenderMarkdownIsXSSSafe(t *testing.T) {
	attacks := []string{
		`<script>alert(1)</script>`,
		`[x](javascript:alert(1))`,
		`[x](//evil.example)`,
		`[x](https://a.ke" onmouseover="alert(1))`,
		`<img src=x onerror=alert(1)>`,
		`**<b>bold</b>**`,
		`[<svg onload=alert(1)>](/ok)`,
	}
	for _, a := range attacks {
		out := string(RenderMarkdown(a))
		if strings.Contains(out, "<script") || strings.Contains(out, "<img") || strings.Contains(out, "<svg") ||
			strings.Contains(out, "<b>") || strings.Contains(out, `href="javascript`) || strings.Contains(out, `href="//`) ||
			strings.Contains(out, "onmouseover=\"") {
			t.Errorf("unsafe output for %q: %s", a, out)
		}
	}
}

func TestUnclosedBoldIsLiteral(t *testing.T) {
	if got := string(RenderMarkdown("price **from 500")); got != "<p>price **from 500</p>\n" {
		t.Errorf("got %q", got)
	}
}
