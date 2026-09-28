package markdown

import "testing"

// The Python ldo's own cases, with its expected Markdown.
func TestHTMLBecomesMarkdown(t *testing.T) {
	cases := map[string][2]string{
		"headings": {"<h1>Runbook</h1><h3>Steps</h3>", "# Runbook\n\n### Steps"},
		"emphasis": {"<p>Use <strong>care</strong>, <em>read</em> and <code>ls</code>.</p>", "Use **care**, _read_ and `ls`."},
		"links": {`<p>See <a href="https://a.example/x">the docs</a> or <a href="https://b.example">https://b.example</a></p>`,
			"See [the docs](https://a.example/x) or https://b.example"},
		"lists":   {"<ul><li><p>one</p></li><li>two<ul><li>nested</li></ul></li></ul>", "- one\n- two\n  - nested"},
		"ordered": {"<ol><li>first</li><li>second</li></ol>", "1. first\n2. second"},
		"table": {"<table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>x | y</td></tr></table>",
			"| A | B |\n| --- | --- |\n| 1 | x \\| y |"},
		"layout-cell": {"<table><tr><td><h2>Card</h2><p>text</p><ul><li>a</li><li>b</li></ul></td></tr></table>", "| Card text - a - b |\n| --- |"},
		"pre":         {"<pre>keep   this\n  as is</pre>", "```\nkeep   this\n  as is\n```"},
		"code-macro": {`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">bash</ac:parameter>` +
			"<ac:plain-text-body><![CDATA[echo  hi]]></ac:plain-text-body></ac:structured-macro>", "```\necho  hi\n```"},
		"tasks":             {"<ac:task-list><ac:task><ac:task-body>call</ac:task-body></ac:task></ac:task-list>", "- [ ] call"},
		"quote-break-rule":  {"<blockquote>quoted</blockquote><p>a<br/>b</p><hr/><p>&lt;x&gt; &amp; y</p>", "> quoted\n\na  \nb\n\n---\n\n<x> & y"},
		"images-and-hidden": {`<p><img alt="diagram"/><img/> <span>kept</span> <script>no()</script></p>`, "[diagram] kept"},
		"unclosed":          {"<p>unclosed <a href='https://c.example'>link", "unclosed link"},
		"alone":             {"<li>alone</li>", "- alone"},
	}
	for name, test := range cases {
		if got := FromHTML(test[0]); got != test[1]+"\n" {
			t.Errorf("%s:\ngot  %q\nwant %q", name, got, test[1]+"\n")
		}
	}
}
