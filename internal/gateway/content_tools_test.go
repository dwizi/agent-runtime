package gateway

import (
	"strings"
	"testing"
)

func TestConvertHTMLToMarkdown(t *testing.T) {
	html := `<html>
<body>
<h1>Title</h1>
<p>Hello <b>World</b></p>
<a href="https://example.com">Link</a>
<ul><li>Item 1</li><li>Item 2</li></ul>
</body>
</html>`

	md := convertHTMLToMarkdown(html)
	
	if !strings.Contains(md, "# Title") {
		t.Errorf("missing title in %q", md)
	}
	if !strings.Contains(md, "Hello World") {
		t.Errorf("missing text in %q", md)
	}
	if !strings.Contains(md, "[Link](https://example.com)") {
		t.Errorf("missing link in %q", md)
	}
	if !strings.Contains(md, "- Item 1") {
		t.Errorf("missing list in %q", md)
	}
}
