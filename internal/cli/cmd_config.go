package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/atlassian"
	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/config"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/microsoft"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
)

// Templates are each vendor's part of the file config init writes, after the shared
// settings; a vendor's package adds its own.
var Templates = []string{microsoft.ConfigTemplate, servicenow.ConfigTemplate, atlassian.ConfigTemplate}

// configCommand is config: init and path.
func configCommand(rt *Runtime) *cobra.Command {
	group := newGroup("config", "Create or locate the config file.")
	force := false
	initCommand := &cobra.Command{
		Use:   "init",
		Short: "Write a config template, with example Microsoft profiles to fill in.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return writeTemplate(rt, force)
		},
	}
	initCommand.Flags().BoolVar(&force, "force", false, "Overwrite an existing file.")
	pathCommand := &cobra.Command{
		Use:   "path",
		Short: "Print the path of the config file in use.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			rt.Console.Println(rt.ConfigPathInUse())
			return nil
		},
	}
	group.AddCommand(initCommand, pathCommand)
	return group
}

func writeTemplate(rt *Runtime, force bool) error {
	path := rt.ConfigPathInUse()
	showBanner(rt, false)
	if _, err := os.Stat(path); err == nil && !force {
		return errs.Configf("%s already exists", path).WithHint("pass --force to overwrite it")
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return errs.Configf("cannot read %s: %v", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return errs.Configf("cannot make %s: %v", filepath.Dir(path), err)
	}
	// Created 0600, rather than written and then narrowed, so no other account can read
	// it even for a moment. The template holds no secrets, but the ids filled in later are
	// nobody else's business.
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errs.Configf("cannot write %s: %v", path, err)
	}
	text := strings.Join(append([]string{config.Header}, Templates...), "\n")
	_, writeErr := handle.WriteString(text)
	if closeErr := handle.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return errs.Configf("cannot write %s: %v", path, writeErr)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, 0o600) // open keeps an existing file's mode, so --force narrows it here
	}
	rt.Console.Println("Wrote " + path)
	rt.Console.Note("Next: replace the placeholder ids, then run %s.", brand.Suggest("profiles"))
	return nil
}
