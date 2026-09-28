package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The release sets the version into the binary at build time, as the justfile's dist
// recipe does, and a binary built that way says it: the linker can set a string only when
// nothing else gives it a value, which a change to the brand package could undo unseen.
func TestAReleaseBuildSaysItsVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not on PATH")
	}
	justfile := readFile(t, filepath.Join(root(t), "justfile"))
	flag := regexp.MustCompile(`-X (\S+)=\{\{version\}\}`).FindStringSubmatch(justfile)
	if flag == nil {
		t.Fatal("the dist recipe sets no version")
	}
	binary := filepath.Join(t.TempDir(), "ldo-go")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command(goTool, "build", "-ldflags", "-X "+flag[1]+"=9.8.7", "-o", binary, "./cmd/ldo-go")
	build.Dir = root(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := exec.Command(binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "ldo-go 9.8.7" {
		t.Errorf("%q %v", out, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
