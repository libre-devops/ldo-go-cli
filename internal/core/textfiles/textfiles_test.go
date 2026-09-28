package textfiles

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

func names(t *testing.T, folder string) string {
	t.Helper()
	entries, _ := os.ReadDir(folder)
	var found []string
	for _, entry := range entries {
		found = append(found, entry.Name())
	}
	return strings.Join(found, ",")
}

func TestAFileIsWrittenBackAsItWasWritten(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "variables.tf")
	_ = os.WriteFile(path, []byte("\xef\xbb\xbfvariable \"a\" {}\r\n"), 0o640)
	file, err := Read(path)
	if err != nil || file.Text != "variable \"a\" {}\r\n" || !file.BOM || file.Newline != "\r\n" {
		t.Fatalf("%+v %v", file, err)
	}
	if err := Write(file, "variable \"b\" {}\r\n"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "\xef\xbb\xbfvariable \"b\" {}\r\n" {
		t.Errorf("%q", data)
	}
	if info, _ := os.Stat(path); runtime.GOOS != "windows" && info.Mode().Perm() != 0o640 {
		t.Error(info.Mode())
	}
	if got := names(t, folder); got != "variables.tf" {
		t.Error(got)
	}
}

func TestANewFileIsMadeAndPlainUTF8HasNoMark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	if err := Write(File{Path: path}, "# Title\n"); err != nil {
		t.Fatal(err)
	}
	if file, _ := Read(path); file != (File{Path: path, Text: "# Title\n", Newline: "\n"}) {
		t.Errorf("%+v", file)
	}
}

func TestWhatCannotBeReadOrWrittenIsAnInputError(t *testing.T) {
	folder := t.TempDir()
	if _, err := Read(filepath.Join(folder, "missing.tf")); !errs.Is(err, errs.Input) || !strings.Contains(err.Error(), "cannot read") {
		t.Error(err)
	}
	binary := filepath.Join(folder, "image.tf")
	_ = os.WriteFile(binary, []byte{0xff, 0xfe}, 0o600)
	if _, err := Read(binary); err == nil || !strings.Contains(err.Error(), "is not UTF-8") {
		t.Error(err)
	}
	if err := Write(File{Path: filepath.Join(folder, "no", "such.tf")}, "x"); err == nil || !strings.Contains(err.Error(), "cannot write in") {
		t.Error(err)
	}
	rename = func(string, string) error {
		return &os.LinkError{Op: "rename", Err: syscall.EACCES}
	}
	defer func() { rename = os.Rename }()
	kept := filepath.Join(folder, "kept.tf")
	_ = os.WriteFile(kept, []byte("before"), 0o600)
	file, _ := Read(kept)
	if err := Write(file, "after"); err == nil || !strings.Contains(err.Error(), "kept.tf: permission denied") {
		t.Error(err)
	}
	if data, _ := os.ReadFile(kept); string(data) != "before" {
		t.Error(string(data))
	}
	if got := names(t, folder); got != "image.tf,kept.tf" {
		t.Error(got)
	}
	if got := reason(errors.New("plain")); got != "plain" {
		t.Error(got)
	}
}
