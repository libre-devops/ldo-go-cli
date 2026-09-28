package jsontext

import (
	"os"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/yamltext"
)

func TestTheOutputIsPythonsJSONDumps(t *testing.T) {
	input, _ := os.ReadFile("testdata/in.json")
	data, err := yamltext.Decode(input, true)
	if err != nil {
		t.Fatal(err)
	}
	for file, indent := range map[string]int{"testdata/out-line.json": 0, "testdata/out-indent.json": 2} {
		want, _ := os.ReadFile(file)
		if got := Dumps(data, indent); got != string(want) {
			t.Errorf("%s:\ngot  %s\nwant %s", file, got, want)
		}
	}
}

func TestMapsSortTheirKeys(t *testing.T) {
	if got := Dumps(map[string]any{"b": []string{"x"}, "a": 2, "c": int64(3)}, 0); got != `{"a": 2, "b": ["x"], "c": 3}` {
		t.Error(got)
	}
	if got := Dumps(struct{ A int }{1}, 0); got != `{"A":1}` {
		t.Error(got)
	}
	if Float(1e16) != "1e+16" || Float(-0.5) != "-0.5" || Number("7") != "7" {
		t.Error(Float(1e16))
	}
	if Number("1e400") != "Infinity" || Number("-1e400") != "-Infinity" || Number("1e-400") != "0.0" || Number("1e+x") != "1e+x" {
		t.Error(Number("1e400"), Number("1e-400"))
	}
}
