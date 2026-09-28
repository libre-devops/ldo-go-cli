package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONPrettyKeepsOrderAndWritesPythonsLayout(t *testing.T) {
	h := newHarness(t, nil)
	h.stdin = `{"b": 1, "a": [1.50, "caf\u00e9", null, {}], "c": {"d": true}}`
	if out := h.ok("json"); out != "{\n  \"b\": 1,\n  \"a\": [\n    1.5,\n    \"café\",\n    null,\n    {}\n  ],\n  \"c\": {\n    \"d\": true\n  }\n}\n" {
		t.Errorf("%q", out)
	}
	if out := h.ok("json", "--compact", "--sort-keys"); out != "{\"a\":[1.5,\"café\",null,{}],\"b\":1,\"c\":{\"d\":true}}\n" {
		t.Errorf("%q", out)
	}
	if out := h.ok("json", "--indent", "0"); out != "{\n\"b\": 1,\n\"a\": [\n1.5,\n\"café\",\nnull,\n{}\n],\n\"c\": {\n\"d\": true\n}\n}\n" {
		t.Errorf("%q", out)
	}
	if out := h.ok("json", "--yaml", "--sort-keys"); out != "a:\n  - 1.5\n  - café\n  - null\n  - {}\nb: 1\nc:\n  d: true\n" {
		t.Errorf("%q", out)
	}
	contains(t, h.ok("json", "--colour"), "\x1b[")
}

func TestJSONLinesAndMistakes(t *testing.T) {
	h := newHarness(t, nil)
	h.stdin = "{\"a\": 1}\n\n{\"b\": 2}\n"
	if out := h.ok("json", "-c"); out != "{\"a\":1}\n{\"b\":2}\n" {
		t.Errorf("%q", out)
	}
	if out := h.ok("json", "--yaml"); out != "a: 1\n---\nb: 2\n" {
		t.Errorf("%q", out)
	}
	h.stdin = "{\"a\": 1,\n  oops}"
	contains(t, h.fails(1, "json"), "not JSON:", "at line 2, column 3")
	h.stdin = "{\"a\": 1}\n{\"b\": oops}\n"
	contains(t, h.fails(1, "json"), "not JSON: extra data after the JSON value at line 2, column 1")
	h.stdin = "\"cut"
	contains(t, h.fails(1, "json"), "not JSON: unexpected EOF")
	h.stdin = "  "
	contains(t, h.fails(1, "json"), "the input is empty")
	h.stdin = ""
	contains(t, h.fails(1, "json"), "no JSON to show")
	contains(t, h.fails(1, "json", "/nowhere.json"), "no such file")
	contains(t, h.fails(1, "json", "--yaml", "--compact", writeTemp(t, "[1]")), "--compact is for JSON")
	contains(t, usageError(h.fails(2, "json", "--indent", "9", writeTemp(t, "[1]"))), "--indent")
}

func writeTemp(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "in.json")
	_ = os.WriteFile(path, []byte(text), 0o600)
	return path
}
