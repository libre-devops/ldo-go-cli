package fields

import (
	"encoding/json"
	"testing"
)

func decode(t *testing.T, text string) Object {
	t.Helper()
	var data Object
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestOddValuesBecomePlainDefaults(t *testing.T) {
	data := decode(t, `{"name": "web01", "none": null, "zero": 0, "no": false, "n": 2.5,
		"digits": "3600", "list": ["a", 1, "b"], "objects": [{"a": 1}, "x"], "obj": {"k": "v"},
		"when": "2026-09-24T10:00:00Z", "unknown": "0001-01-01T00:00:00Z"}`)
	if Text(data, "name") != "web01" || Text(data, "none") != "" || Text(data, "missing") != "" {
		t.Fatal("text")
	}
	if Text(data, "zero") != "0" || Text(data, "no") != "false" || Text(data, "n") != "2.5" {
		t.Fatalf("text of %v", data)
	}
	if Map(data["obj"])["k"] != "v" || len(Map(data["name"])) != 0 {
		t.Fatal("map")
	}
	if len(Items(data["list"])) != 3 || Items(data["obj"]) != nil {
		t.Fatal("items")
	}
	if got := Strings(data["list"]); len(got) != 2 || got[1] != "b" {
		t.Fatalf("strings %v", got)
	}
	if len(Objects(data["objects"])) != 1 {
		t.Fatal("objects")
	}
	if flag, ok := Flag(data["no"]); !ok || flag {
		t.Fatal("flag")
	}
	if _, ok := Flag(data["zero"]); ok {
		t.Fatal("a number read as a flag")
	}
	if n, ok := Number(data["digits"]); !ok || n != 3600 {
		t.Fatal("digits")
	}
	if _, ok := Number(data["no"]); ok {
		t.Fatal("a boolean read as a number")
	}
	if Int(data["n"]) != 2 {
		t.Fatal("int")
	}
	if When(data, "when").IsZero() || !When(data, "unknown").IsZero() {
		t.Fatal("when")
	}
}
