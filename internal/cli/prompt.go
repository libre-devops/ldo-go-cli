package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"

	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/webbrowser"
)

// interactive reports whether someone is there to answer a question.
func (r *Runtime) interactive() bool {
	if r.Interactive != nil {
		return r.Interactive()
	}
	return r.StdinIsTerminal && colour.IsTerminal(r.Console.ErrFile)
}

// ask asks the person a question on stderr, and reads the answer from stdin: hidden, with
// hide, as a password is. Ctrl-C ends it, with the terminal as it was.
func (r *Runtime) ask(question string, hide bool) (string, error) {
	if r.Ask != nil {
		return r.Ask(question, hide)
	}
	prompt := question + ": "
	if r.Console.ColourErr() {
		prompt = colour.Style(question, "cyan", false, false) + ": "
	}
	fmt.Fprint(r.Console.Err, prompt)
	type answer struct {
		text string
		err  error
	}
	answered := make(chan answer, 1)
	fd := int(os.Stdin.Fd())
	var state *term.State
	if hide {
		state, _ = term.GetState(fd)
	}
	go func() {
		if hide {
			data, err := term.ReadPassword(fd)
			answered <- answer{string(data), err}
			return
		}
		line, err := bufio.NewReader(r.Stdin).ReadString('\n')
		if errors.Is(err, io.EOF) && line != "" {
			err = nil
		}
		answered <- answer{strings.TrimRight(line, "\r\n"), err}
	}()
	select {
	case got := <-answered:
		if hide {
			fmt.Fprintln(r.Console.Err)
		}
		return got.text, got.err
	case <-r.Ctx().Done():
		if state != nil {
			_ = term.Restore(fd, state)
		}
		fmt.Fprintln(r.Console.Err)
		return "", r.Ctx().Err()
	}
}

// hasBrowser reports whether a browser can be opened here.
func (r *Runtime) hasBrowser() bool {
	if r.HasBrowser != nil {
		return r.HasBrowser()
	}
	return webbrowser.CanLaunch(r.Env, r.lookPath())
}

// lookPath finds a tool on PATH.
func (r *Runtime) lookPath() func(string) (string, error) {
	if r.LookPath != nil {
		return r.LookPath
	}
	return exec.LookPath
}

func (r *Runtime) openBrowser(link string) error {
	if r.OpenBrowser != nil {
		return r.OpenBrowser(link)
	}
	return webbrowser.Open(link)
}
