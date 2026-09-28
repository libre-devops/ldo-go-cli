package yamltext

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTheOutputIsThePythonWritersOwn(t *testing.T) {
	input, err := os.ReadFile("testdata/input.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/output.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Decode(input, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := Dumps(data, 2, nil); got != string(want) {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestMapsAreSortedAndOrderedKeepsItsOrder(t *testing.T) {
	got := Dumps(Ordered{{"z", 1}, {"a", map[string]any{"y": true, "b": []string{"x"}}}}, 4, nil)
	if got != "z: 1\na:\n    b:\n        - x\n    \"y\": true\n" {
		t.Errorf("%q", got)
	}
}

func TestPaintSeesEachKind(t *testing.T) {
	var kinds []string
	Dumps(map[string]any{"k": []any{"s", 1.5, false, nil}}, 2, func(kind, text string) string {
		kinds = append(kinds, kind)
		return text
	})
	if strings.Join(kinds, ",") != "key,punct,punct,string,punct,number,punct,bool,punct,null" {
		t.Error(kinds)
	}
}

func TestFloatsAsPythonWritesThem(t *testing.T) {
	for value, want := range map[float64]string{1: "1.0", 0.1: "0.1", 1e16: "1.0e+16", 1.5e-7: "1.5e-07", 12345.678: "12345.678",
		-2.5e300: "-2.5e+300"} {
		if got := float(value); got != want {
			t.Errorf("%v: %s", value, got)
		}
	}
	if number("12") != "12" || number("1.50") != "1.5" || number("99999999999999999999") != "99999999999999999999" {
		t.Error("numbers")
	}
	// Too big for a float is infinity, as Python's json reads it.
	if number("1e400") != "Infinity" || number("-1e400") != "-Infinity" || number("-1e-400") != "-0.0" || number("1e+x") != "1e+x" {
		t.Error(number("1e400"), number("-1e-400"))
	}
}

func TestDecodeKeepsOrderAndRefusesTrailingData(t *testing.T) {
	data, err := Decode([]byte(`{"b": {"d": 1, "c": [true]}, "a": null}`), false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(data)
	if string(encoded) != `{"b":{"d":1,"c":[true]},"a":null}` {
		t.Error(string(encoded))
	}
	if value, ok := data.(Ordered).Get("a"); !ok || value != nil {
		t.Error(value)
	}
	for _, bad := range []string{`{"a": 1} {}`, `{"a": }`, `[1,`, ``} {
		if _, err := Decode([]byte(bad), false); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}
