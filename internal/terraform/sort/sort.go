// Package sort puts a module's variables and outputs in name order: the blocks of one kind
// moved into order where they stand, the comments just above each moving with it, and all
// else left as it was. Local files only: nothing to sign in to.
package sort

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/textfiles"
	"github.com/libre-devops/ldo-go-cli/internal/terraform"
)

// Kinds are the kinds of block sorted: a module's inputs and its outputs.
var Kinds = []string{"variable", "output"}

// Files are where a module keeps each kind, by convention.
var Files = map[string]string{"variable": "variables.tf", "output": "outputs.tf"}

// Sorting is what sorting one kind of block in one file found: how many, whether they
// were in order already, and whether the file was written to put them in order.
type Sorting struct {
	Path    string
	Kind    string
	Count   int
	InOrder bool
	Written bool
}

// Compare is how names sort: ignoring case, then as written, a character at a time, as
// terraform-docs orders a README's inputs and outputs, so a file and its README agree.
func Compare(a, b string) int {
	if order := strings.Compare(strings.ToLower(a), strings.ToLower(b)); order != 0 {
		return order
	}
	return strings.Compare(a, b)
}

// Text is text with its kind blocks in name order, each where one of them stood; how many
// there are; and whether they were in order already (text itself, then).
func Text(text, kind string) (string, int, bool, error) {
	pieces, err := terraform.Split(text)
	if err != nil {
		return "", 0, false, err
	}
	var slots []int
	var blocks []terraform.Piece
	for at, piece := range pieces {
		if piece.Kind == kind {
			slots = append(slots, at)
			blocks = append(blocks, piece)
		}
	}
	ordered := slices.Clone(blocks)
	slices.SortStableFunc(ordered, func(a, b terraform.Piece) int { return Compare(a.Name, b.Name) })
	if slices.EqualFunc(ordered, blocks, func(a, b terraform.Piece) bool { return a.Name == b.Name }) {
		return text, len(blocks), true, nil
	}
	for index, at := range slots {
		pieces[at] = ordered[index]
	}
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	var sorted strings.Builder
	for _, piece := range pieces {
		sorted.WriteString(ended(piece, newline))
	}
	return sorted.String(), len(blocks), false, nil
}

// ended is a piece's text ending in a line break, so a block moved up from the end of a
// file that has none still starts the next one on a line of its own.
func ended(piece terraform.Piece, newline string) string {
	if strings.HasSuffix(piece.Text, "\n") {
		return piece.Text
	}
	return piece.Text + newline
}

// File sorts each of kinds of block in the file at path, writing it only when write and
// something moved: what each found.
func File(path string, kinds []string, write bool) ([]Sorting, error) {
	file, err := textfiles.Read(path)
	if err != nil {
		return nil, err
	}
	text := file.Text
	var found []Sorting
	for _, kind := range kinds {
		sorted, count, inOrder, err := Text(text, kind)
		if err != nil {
			failure := errs.As(err)
			return nil, errs.Inputf("%s: %s", path, failure.Message).WithHint("%s", failure.Hint)
		}
		text = sorted
		found = append(found, Sorting{Path: path, Kind: kind, Count: count, InOrder: inOrder, Written: write && !inOrder})
	}
	if write && text != file.Text {
		if err := textfiles.Write(file, text); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// Target is a file to sort, and the kinds of block to sort in it.
type Target struct {
	Path  string
	Kinds []string
}

// Targets are the files to sort: a file named, for all of kinds; a folder's
// variables.tf for its variables and outputs.tf for its outputs, and with recursive
// those of every folder beneath it (examples/, modules/).
func Targets(paths, kinds []string, recursive bool) ([]Target, error) {
	var found []Target
	for _, path := range paths {
		info, err := os.Stat(path)
		switch {
		case err == nil && !info.IsDir():
			found = append(found, Target{Path: path, Kinds: kinds})
			continue
		case err != nil:
			return nil, errs.Inputf("%s is not a file or a folder", path)
		}
		folders, err := walk(path, recursive)
		if err != nil {
			return nil, err
		}
		for _, folder := range folders {
			for _, kind := range kinds {
				if file := filepath.Join(folder, Files[kind]); isFile(file) {
					found = append(found, Target{Path: file, Kinds: []string{kind}})
				}
			}
		}
	}
	if len(found) == 0 {
		var names []string
		for _, kind := range kinds {
			names = append(names, Files[kind])
		}
		return nil, errs.Inputf("no %s to sort there", strings.Join(names, " or ")).WithHint("name the .tf files to sort")
	}
	return found, nil
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// walk is root, and with recursive every folder beneath it but hidden ones, such as
// .terraform (downloaded modules, not this one's) and .git.
func walk(root string, recursive bool) ([]string, error) {
	if !recursive {
		return []string{root}, nil
	}
	var found []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if path != root && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		return nil, errs.Inputf("cannot read %s: %v", root, err)
	}
	return found, nil
}
