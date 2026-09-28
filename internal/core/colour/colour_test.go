package colour

import (
	"os"
	"strings"
	"testing"
)

func TestTheDecisionIsAskedThenTheEnvironment(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	Use(nil)
	if Setting() != nil || Wanted(nil) {
		t.Fatal("no terminal, no colour")
	}
	t.Setenv("FORCE_COLOR", "1")
	if !Wanted(nil) {
		t.Fatal("FORCE_COLOR")
	}
	t.Setenv("NO_COLOR", "1")
	if Wanted(nil) {
		t.Fatal("NO_COLOR wins over FORCE_COLOR")
	}
	on := true
	Use(&on)
	defer Use(nil)
	if !Wanted(nil) {
		t.Fatal("--colour wins over NO_COLOR")
	}
}

func TestStyles(t *testing.T) {
	t.Setenv("COLORTERM", "")
	t.Setenv("WT_SESSION", "")
	os.Unsetenv("WT_SESSION") // present at all, empty or not, is Windows Terminal
	if Style("x", nil, false, false) != "x" {
		t.Fatal("plain")
	}
	if Style("x", "green", true, false) != "\x1b[32m\x1b[1mx\x1b[0m" {
		t.Fatalf("%q", Style("x", "green", true, false))
	}
	if Style("x", "bright_black", false, false) != "\x1b[90mx\x1b[0m" {
		t.Fatal("bright")
	}
	if !strings.Contains(Style("x", "#1E3A8A", false, false), "38;5;") || Nearest("#000000") != 16 {
		t.Fatal("hex")
	}
	t.Setenv("COLORTERM", "truecolor")
	if !strings.Contains(Style("x", "#1E3A8A", false, false), "38;2;30;58;138") {
		t.Fatal("truecolour")
	}
	if Strip(Diagonal("unicorn", 1)) != "unicorn" {
		t.Fatal("diagonal")
	}
}

func TestJSONIsColouredButUnchangedUnderneath(t *testing.T) {
	text := "{\n  \"name\": \"a \\\"quoted\\\" b\",\n  \"n\": -1.5e3,\n  \"on\": true,\n  \"off\": false,\n  \"none\": null,\n  \"list\": [\n    {}\n  ]\n}"
	coloured := JSON(text)
	if Strip(coloured) != text {
		t.Fatalf("changed:\n%s", Strip(coloured))
	}
	if !strings.Contains(coloured, Paint("key", `"name"`)) || !strings.Contains(coloured, Paint("number", "-1.5e3")) {
		t.Fatal("not painted")
	}
}
