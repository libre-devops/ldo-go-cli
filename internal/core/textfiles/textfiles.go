// Package textfiles changes text files in place as they were written: UTF-8 with or
// without a byte order mark, either line ending, and a whole new file renamed into place
// so nothing ever reads half of one.
package textfiles

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

const bom = "\xef\xbb\xbf"

// File is a file's text (without a byte order mark), and how it was written.
type File struct {
	Path    string
	Text    string
	BOM     bool
	Newline string
}

// Read is the file at path: an Input error when it cannot be read, or is not UTF-8.
func Read(path string) (File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return File{}, errs.Inputf("cannot read %s: %s", path, reason(err))
	}
	if !utf8.Valid(data) {
		return File{}, errs.Inputf("%s is not UTF-8 text", path)
	}
	text, marked := strings.CutPrefix(string(data), bom)
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	return File{Path: path, Text: text, BOM: marked, Newline: newline}, nil
}

// rename moves the finished file into place; tests replace it to fail.
var rename = os.Rename

// Write replaces file with text, keeping its byte order mark and its permissions. The
// text goes to a temporary file beside it first, renamed over it once complete.
func Write(file File, text string) error {
	folder := filepath.Dir(file.Path)
	temporary, err := os.CreateTemp(folder, "."+filepath.Base(file.Path)+".*.tmp")
	if err != nil {
		return errs.Inputf("cannot write in %s: %s", folder, reason(err))
	}
	name := temporary.Name()
	done := false
	defer func() {
		if !done {
			_ = os.Remove(name)
		}
	}()
	if file.BOM {
		text = bom + text
	}
	if _, err := temporary.WriteString(text); err != nil {
		_ = temporary.Close()
		return errs.Inputf("cannot write %s: %s", file.Path, reason(err))
	}
	if err := temporary.Close(); err != nil {
		return errs.Inputf("cannot write %s: %s", file.Path, reason(err))
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(file.Path); err == nil {
		mode = info.Mode().Perm()
	}
	// A file system without modes keeps its own.
	_ = os.Chmod(name, mode)
	if err := rename(name, file.Path); err != nil {
		return errs.Inputf("cannot write %s: %s", file.Path, reason(err))
	}
	done = true
	return nil
}

// reason is an operating system error without the path it repeats.
func reason(err error) string {
	var path *fs.PathError
	if errors.As(err, &path) {
		return path.Err.Error()
	}
	var link *os.LinkError
	if errors.As(err, &link) {
		return link.Err.Error()
	}
	return err.Error()
}
