package process

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

type fake struct {
	result Result
	err    error
	seen   []Request
	wait   bool
}

func (f *fake) Run(ctx context.Context, request Request) (Result, error) {
	f.seen = append(f.seen, request)
	if f.wait {
		<-ctx.Done()
	}
	return f.result, f.err
}

func found(string) (string, error) { return "/bin/tool", nil }

func TestARunGivesItsOutput(t *testing.T) {
	runner := &fake{result: Result{Stdout: `{"a": 1}`}}
	command := &Command{Name: "tool", Runner: runner, LookPath: found, Env: func() []string { return []string{"A=1"} }}
	value, err := command.RunJSON(context.Background(), "show", "--flag")
	if err != nil || value.(map[string]any)["a"] != float64(1) {
		t.Errorf("%v %v", value, err)
	}
	if runner.seen[0].Path != "/bin/tool" || runner.seen[0].Env[0] != "A=1" {
		t.Errorf("%+v", runner.seen[0])
	}
	runner.result.Stdout = " "
	if value, err := command.RunJSON(context.Background(), "show"); value != nil || err != nil {
		t.Errorf("%v %v", value, err)
	}
	runner.result.Stdout = "not json"
	if _, err := command.RunJSON(context.Background(), "show"); err == nil || err.Error() != "tool show did not return JSON" {
		t.Errorf("%v", err)
	}
}

func TestAFailedRunSaysWhyWithAHint(t *testing.T) {
	runner := &fake{result: Result{ExitCode: 2, Stderr: "\n  bad thing\n\n"}}
	command := &Command{Name: "tool", Runner: runner, LookPath: found, Kind: errs.Auth,
		HintFor: func(detail string) string { return "fix " + detail }}
	_, err := command.Run(context.Background(), "go", "--now")
	if !errs.Is(err, errs.Auth) || err.Error() != "tool go failed: bad thing" || errs.HintOf(err) != "fix bad thing" {
		t.Errorf("%v (%s)", err, errs.HintOf(err))
	}
	runner.result.Stderr = ""
	if _, err := command.RunWith(context.Background(), []string{"B=2"}, "go"); !strings.Contains(err.Error(), "exit code 2") {
		t.Errorf("%v", err)
	}
	runner.err, runner.result = errors.New("no such file"), Result{}
	if err := command.RunInteractive(context.Background(), nil, "login"); !strings.Contains(err.Error(), "cannot run tool login") {
		t.Errorf("%v", err)
	}
	if !runner.seen[len(runner.seen)-1].Interactive {
		t.Error("not interactive")
	}
}

func TestARunThatHangsTimesOut(t *testing.T) {
	command := &Command{Name: "tool", Runner: &fake{wait: true}, LookPath: found, Timeout: time.Millisecond}
	if _, err := command.Run(context.Background(), "wait"); err == nil || err.Error() != "tool wait timed out" {
		t.Errorf("%v", err)
	}
}

func TestAMissingToolSaysHowToInstallIt(t *testing.T) {
	command := &Command{Name: "tool", InstallHint: "install it", LookPath: func(string) (string, error) {
		return "", errors.New("not found")
	}}
	if _, err := command.Run(context.Background()); errs.HintOf(err) != "install it" || !strings.Contains(err.Error(), "not on PATH") {
		t.Errorf("%v", err)
	}
}

func TestExecRunsARealProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sh")
	}
	result, err := Exec{}.Run(context.Background(), Request{Path: "/bin/sh", Args: []string{"-c", "echo out; echo err >&2; exit 3"}})
	if err != nil || result.Stdout != "out\n" || result.Stderr != "err\n" || result.ExitCode != 3 {
		t.Errorf("%+v %v", result, err)
	}
}

func TestLongStderrIsCut(t *testing.T) {
	command := &Command{Name: "tool", Runner: &fake{result: Result{ExitCode: 1, Stderr: strings.Repeat("x", 2000)}}, LookPath: found}
	if _, err := command.Run(context.Background()); len(err.Error()) > 1100 {
		t.Errorf("%d", len(err.Error()))
	}
}
