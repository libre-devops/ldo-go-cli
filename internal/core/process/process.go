// Package process runs an external command line tool: checked exit codes, a timeout, and
// readable errors. Vendor layers build on Command for their own tools (the Azure CLI,
// terraform, terraform-docs), saying how that tool's errors read and what to suggest.
// Output is returned to the caller but never logged, because it can hold a token.
package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Request is one run of a program.
type Request struct {
	Path string
	Args []string
	// Env is added to the process's own environment.
	Env []string
	// Interactive connects the terminal instead of capturing output.
	Interactive bool
	Dir         string
}

// Result is what a run gave back.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs a program; tests give a fake, so nothing real runs.
type Runner interface {
	Run(ctx context.Context, request Request) (Result, error)
}

// Exec runs programs for real.
type Exec struct{}

// Run runs request's program and waits for it.
func (Exec) Run(ctx context.Context, request Request) (Result, error) {
	command := exec.CommandContext(ctx, request.Path, request.Args...)
	command.Env = append(os.Environ(), request.Env...)
	command.Dir = request.Dir
	var stdout, stderr bytes.Buffer
	if request.Interactive {
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	} else {
		command.Stdout, command.Stderr = &stdout, &stderr
	}
	err := command.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}
	return result, err
}

// LookPath finds a program on PATH; tests give their own.
type LookPath func(name string) (string, error)

// Command runs one program, found on PATH by Name unless Executable is set.
type Command struct {
	Name        string
	Executable  string
	InstallHint string
	Runner      Runner
	LookPath    LookPath
	// Env is added to every run's environment.
	Env func() []string
	// Timeout bounds a run that is not interactive; two minutes when zero.
	Timeout time.Duration
	// CleanStderr is the error message from stderr; its lines joined when nil.
	CleanStderr func(string) string
	// HintFor is a next step for a failed run, when the message points to one.
	HintFor func(string) string
	// Kind is the error kind a failure is; Command when zero.
	Kind errs.Kind
}

func (c *Command) fail(format string, args ...any) *errs.Error {
	err := errs.Commandf(format, args...)
	if c.Kind != 0 {
		err.Kind = c.Kind
	}
	return err
}

// Path is the program's path, found on first use so that nothing needs it installed
// until it is run.
func (c *Command) Path() (string, error) {
	if c.Executable != "" {
		return c.Executable, nil
	}
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	found, err := look(c.Name)
	if err != nil {
		return "", c.fail("'%s' is not on PATH", c.Name).WithHint("%s", c.InstallHint)
	}
	c.Executable = found
	return found, nil
}

// Run runs the program with args and returns its stdout: an error on a non-zero exit.
func (c *Command) Run(ctx context.Context, args ...string) (string, error) {
	return c.run(ctx, false, nil, args)
}

// RunWith runs the program with args and extra environment.
func (c *Command) RunWith(ctx context.Context, env []string, args ...string) (string, error) {
	return c.run(ctx, false, env, args)
}

// RunInteractive runs the program on the terminal, for one that asks a person something
// (a sign-in); it is not timed out.
func (c *Command) RunInteractive(ctx context.Context, env []string, args ...string) error {
	_, err := c.run(ctx, true, env, args)
	return err
}

func (c *Command) run(ctx context.Context, interactive bool, extra []string, args []string) (string, error) {
	path, err := c.Path()
	if err != nil {
		return "", err
	}
	label := c.label(args)
	slog.Debug("running " + label)
	if !interactive {
		timeout := c.Timeout
		if timeout == 0 {
			timeout = 2 * time.Minute
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var env []string
	if c.Env != nil {
		env = c.Env()
	}
	runner := c.Runner
	if runner == nil {
		runner = Exec{}
	}
	result, err := runner.Run(ctx, Request{Path: path, Args: args, Env: append(env, extra...), Interactive: interactive})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", c.fail("%s timed out", label)
	}
	if err != nil {
		return "", c.fail("cannot run %s: %v", label, err)
	}
	if result.ExitCode != 0 {
		detail := c.clean(result.Stderr)
		if detail == "" {
			detail = "exit code " + strconv.Itoa(result.ExitCode)
		}
		failure := c.fail("%s failed: %s", label, detail)
		if c.HintFor != nil {
			failure.Hint = c.HintFor(detail)
		}
		return "", failure
	}
	return result.Stdout, nil
}

// RunJSON runs the program and reads its stdout as JSON (nil for no output).
func (c *Command) RunJSON(ctx context.Context, args ...string) (any, error) {
	out, err := c.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal([]byte(out), &value); err != nil {
		return nil, c.fail("%s did not return JSON", c.label(args))
	}
	return value, nil
}

func (c *Command) label(args []string) string {
	words := []string{c.Name}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break
		}
		words = append(words, arg)
	}
	return strings.Join(words, " ")
}

func (c *Command) clean(stderr string) string {
	if c.CleanStderr != nil {
		return c.CleanStderr(stderr)
	}
	var lines []string
	for _, line := range strings.Split(stderr, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	joined := strings.Join(lines, " ")
	if len(joined) > 1000 {
		joined = joined[:1000]
	}
	return joined
}
