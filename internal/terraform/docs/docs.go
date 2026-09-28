// Package docs writes a module's README: its hand-written top (HEADER.md) above the
// section terraform-docs writes between its markers, and terraform-docs run to write
// that section. terraform-docs is not part of this tool: it is found on PATH. Local files
// only: nothing to sign in to.
package docs

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/process"
	"github.com/libre-devops/ldo-go-cli/internal/core/textfiles"
)

// The README's markers, and the files a module keeps.
const (
	Begin  = "<!-- BEGIN_TF_DOCS -->"
	End    = "<!-- END_TF_DOCS -->"
	Header = "HEADER.md"
	Readme = "README.md"
	config = ".terraform-docs.yml"
)

// Result is what became of one module's README: updated, up to date or, when only
// checked, out of date; and the header file its top came from, if any.
type Result struct {
	Folder string
	Path   string
	// Header is "" when the folder has none.
	Header string
	State  string
}

// WithHeader is readme with header above its terraform-docs section, keeping the section
// and whatever follows it, and ending in a line break. Without a header (nil) its own top
// is kept; either way the section's markers are added when it has none, for terraform-docs
// to write between.
func WithHeader(readme string, header *string, newline string) (string, error) {
	start := strings.Index(readme, Begin)
	if start >= 0 && !strings.Contains(readme[start:], End) {
		return "", errs.Inputf("it has %s but no %s after it", Begin, End)
	}
	top, section := readme, Begin+newline+End+newline
	if start >= 0 {
		top, section = readme[:start], readme[start:]
	}
	if header != nil {
		top = *header
	}
	if !strings.HasSuffix(section, "\n") {
		section += newline
	}
	if strings.TrimSpace(top) == "" {
		return section, nil
	}
	return strings.TrimRight(top, " \t\r\n\f\v") + newline + newline + section, nil
}

// Options are the file names to use in each folder, and whether only to check.
type Options struct {
	Check  bool
	Header string
	Readme string
}

func (o Options) names() (string, string) {
	header, readme := o.Header, o.Readme
	if header == "" {
		header = Header
	}
	if readme == "" {
		readme = Readme
	}
	return header, readme
}

// Document puts the folder's header file (when it has one) at the top of its README and
// runs terraform-docs to write the rest; or, with Check, changes nothing and says whether
// doing so would change the README.
func Document(ctx context.Context, folder string, tool *process.Command, opts Options) (Result, error) {
	headerName, readmeName := opts.names()
	headerPath := filepath.Join(folder, headerName)
	var header *string
	used := ""
	if isFile(headerPath) {
		file, err := textfiles.Read(headerPath)
		if err != nil {
			return Result{}, err
		}
		header, used = &file.Text, headerPath
	}
	readmePath := filepath.Join(folder, readmeName)
	before := textfiles.File{Path: readmePath, Newline: "\n"}
	if isFile(readmePath) {
		var err error
		if before, err = textfiles.Read(readmePath); err != nil {
			return Result{}, err
		}
	}
	wanted, err := WithHeader(before.Text, header, before.Newline)
	if err != nil {
		return Result{}, errs.Inputf("%s: %s", readmePath, errs.As(err).Message)
	}
	result := Result{Folder: folder, Path: readmePath, Header: used}
	if opts.Check {
		fresh := wanted == before.Text
		if fresh {
			if fresh, err = generated(ctx, tool, folder, readmeName); err != nil {
				return Result{}, err
			}
		}
		result.State = "out of date"
		if fresh {
			result.State = "up to date"
		}
		return result, nil
	}
	return write(ctx, tool, before, wanted, result, readmeName)
}

func write(ctx context.Context, tool *process.Command, before textfiles.File, wanted string, result Result, readmeName string) (Result, error) {
	if wanted != before.Text {
		if err := textfiles.Write(before, wanted); err != nil {
			return Result{}, err
		}
	}
	if _, err := tool.Run(ctx, arguments(result.Folder, readmeName, false)...); err != nil {
		return Result{}, err
	}
	after, err := textfiles.Read(result.Path)
	if err != nil {
		return Result{}, err
	}
	result.State = "updated"
	if after.Text == before.Text {
		result.State = "up to date"
	}
	return result, nil
}

// Folders are the module folders to document: each of paths, and with recursive every
// folder beneath one that has a header file of its own (an example, a submodule).
func Folders(paths []string, recursive bool, headerName string) ([]string, error) {
	if headerName == "" {
		headerName = Header
	}
	var found []string
	for _, path := range paths {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			return nil, errs.Inputf("%s is not a folder", path).WithHint("give the module's folder")
		}
		found = append(found, path)
		if recursive {
			beneath, err := foldersBeneath(path, headerName)
			if err != nil {
				return nil, err
			}
			found = append(found, beneath...)
		}
	}
	return found, nil
}

func foldersBeneath(root, headerName string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if isFile(filepath.Join(path, headerName)) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, errs.Inputf("cannot read %s: %v", root, err)
	}
	return found, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// generated reports whether terraform-docs would leave the README's section as it is.
func generated(ctx context.Context, tool *process.Command, folder, readmeName string) (bool, error) {
	_, err := tool.Run(ctx, arguments(folder, readmeName, true)...)
	if err != nil && strings.Contains(err.Error(), "out of date") {
		return false, nil
	}
	return err == nil, err
}

// arguments are terraform-docs' arguments: the module's own .terraform-docs.yml, which
// terraform-docs reads from the module's folder, else a Markdown table injected into the
// README.
func arguments(folder, readmeName string, check bool) []string {
	var args []string
	if !isFile(filepath.Join(folder, config)) {
		args = []string{"markdown", "table", "--output-file", readmeName, "--output-mode", "inject"}
	}
	if check {
		args = append(args, "--output-check")
	}
	return append(args, folder)
}
