package pages

import (
	"html"
	"html/template"
	"strings"
)

// RenderMarkdown converts the restricted Markdown used for managed pages into safe HTML.
// Supported: "## " and "### " headings ("# " becomes h2 — the page title is the only h1),
// paragraphs, "- " / "* " bullet lists, "1. " numbered lists, **bold**, and [links](url)
// where url is a site path, https, mailto: or tel:. Everything else is escaped text;
// raw HTML is never passed through.
func RenderMarkdown(src string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var para []string
	list := "" // "", "ul", "ol"
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>")
			b.WriteString(inline(strings.Join(para, " ")))
			b.WriteString("</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	openList := func(kind string) {
		if list != kind {
			closeList()
			b.WriteString("<" + kind + ">\n")
			list = kind
		}
	}
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flushPara()
			closeList()
		case strings.HasPrefix(line, "### "):
			flushPara()
			closeList()
			b.WriteString("<h3>" + inline(line[4:]) + "</h3>\n")
		case strings.HasPrefix(line, "## "), strings.HasPrefix(line, "# "):
			flushPara()
			closeList()
			b.WriteString("<h2>" + inline(strings.TrimLeft(line, "# ")) + "</h2>\n")
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			flushPara()
			openList("ul")
			b.WriteString("<li>" + inline(line[2:]) + "</li>\n")
		case orderedItem(line) > 0:
			flushPara()
			openList("ol")
			b.WriteString("<li>" + inline(line[orderedItem(line):]) + "</li>\n")
		default:
			closeList()
			para = append(para, line)
		}
	}
	flushPara()
	closeList()
	return template.HTML(b.String()) //nolint:gosec // every text segment is escaped above
}

// orderedItem returns the content offset for "12. text", or 0.
func orderedItem(line string) int {
	i := 0
	for i < len(line) && i < 3 && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' ' {
		return i + 2
	}
	return 0
}

// inline renders **bold** and [text](url) with all other text escaped.
func inline(s string) string {
	var b strings.Builder
	bold := false
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "**"):
			if bold {
				b.WriteString("</strong>")
			} else if strings.Contains(s[i+2:], "**") {
				b.WriteString("<strong>")
			} else {
				b.WriteString("**")
				i += 2
				continue
			}
			bold = !bold
			i += 2
		case s[i] == '[':
			if label, href, n, ok := link(s[i:]); ok {
				attrs := ""
				if strings.HasPrefix(href, "https://") {
					attrs = ` rel="noopener noreferrer"`
				}
				b.WriteString(`<a href="` + html.EscapeString(href) + `"` + attrs + `>` + html.EscapeString(label) + `</a>`)
				i += n
				continue
			}
			b.WriteString("[")
			i++
		default:
			j := i + 1
			for j < len(s) && s[j] != '[' && !strings.HasPrefix(s[j:], "**") {
				j++
			}
			b.WriteString(html.EscapeString(s[i:j]))
			i = j
		}
	}
	if bold {
		b.WriteString("</strong>")
	}
	return b.String()
}

// link parses "[label](href)" at the start of s.
func link(s string) (label, href string, n int, ok bool) {
	end := strings.Index(s, "](")
	if end < 1 {
		return "", "", 0, false
	}
	close := strings.IndexByte(s[end+2:], ')')
	if close < 0 {
		return "", "", 0, false
	}
	label, href = s[1:end], strings.TrimSpace(s[end+2:end+2+close])
	if strings.ContainsAny(label, "[]") || !SafeURL(href) {
		return "", "", 0, false
	}
	return label, href, end + 3 + close, true
}

// SafeURL accepts site paths, https URLs, mailto: and tel: links only.
func SafeURL(u string) bool {
	if u == "" || len(u) > 500 || strings.ContainsAny(u, " \"'<>\\`") {
		return false
	}
	switch {
	case strings.HasPrefix(u, "/"):
		return !strings.HasPrefix(u, "//")
	case strings.HasPrefix(u, "https://"):
		return len(u) > len("https://")
	case strings.HasPrefix(u, "mailto:"), strings.HasPrefix(u, "tel:"):
		return true
	}
	return false
}
