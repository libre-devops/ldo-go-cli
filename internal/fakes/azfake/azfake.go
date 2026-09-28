// Package azfake is a fake Azure CLI for tests: each run is answered from routes, and
// recorded. Nothing in a test runs a real az.
package azfake

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/libre-devops/ldo-go-cli/internal/core/process"
)

// Route answers every az run whose arguments start with Args ("account list").
type Route struct {
	Args   string
	Result process.Result
	Err    error
	// Func answers instead of Result, for a run whose answer changes (az account show
	// after az account set).
	Func func(args []string) process.Result
}

// JSON is a successful run printing value as JSON.
func JSON(value any) process.Result {
	encoded, _ := json.Marshal(value)
	return process.Result{Stdout: string(encoded)}
}

// Failed is a run that exits code with stderr.
func Failed(code int, stderr string) process.Result {
	return process.Result{ExitCode: code, Stderr: stderr}
}

// Runner is the fake az.
type Runner struct {
	routes []Route
	mu     sync.Mutex
	calls  []process.Request
}

// New is a fake az answering from routes: the longest matching prefix wins, and any
// other run fails, so an unexpected one fails its test loudly.
func New(routes ...Route) *Runner { return &Runner{routes: routes} }

// SignedIn is a fake az signed in to accounts, as az account list shows them.
func SignedIn(accounts ...map[string]any) *Runner {
	var current any
	for _, account := range accounts {
		if account["isDefault"] == true {
			current = account
		}
	}
	routes := []Route{{Args: "account list", Result: JSON(accounts)}}
	if current != nil {
		routes = append(routes, Route{Args: "account show", Result: JSON(current)})
	} else {
		routes = append(routes, Route{Args: "account show", Result: Failed(1, "ERROR: Please run 'az login' to setup account.")})
	}
	return New(routes...)
}

// Account is one az account: a subscription in a tenant, the default when isDefault.
func Account(id, tenantID, name, user string, isDefault bool) map[string]any {
	return map[string]any{"id": id, "tenantId": tenantID, "name": name, "isDefault": isDefault,
		"user": map[string]any{"name": user, "type": "user"}, "state": "Enabled"}
}

// Run answers request from the routes.
func (r *Runner) Run(_ context.Context, request process.Request) (process.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, request)
	r.mu.Unlock()
	args := strings.Join(trimmed(request.Args), " ")
	best := -1
	for index, route := range r.routes {
		if (args == route.Args || strings.HasPrefix(args, route.Args+" ")) &&
			(best < 0 || len(route.Args) > len(r.routes[best].Args)) {
			best = index
		}
	}
	if best < 0 {
		return Failed(2, "ERROR: no fake for az "+strconv.Quote(args)), nil
	}
	if r.routes[best].Func != nil {
		return r.routes[best].Func(trimmed(request.Args)), nil
	}
	return r.routes[best].Result, r.routes[best].Err
}

// trimmed is args without the output options every JSON run adds.
func trimmed(args []string) []string {
	var kept []string
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "--only-show-errors":
		case args[index] == "--output" && index+1 < len(args):
			index++
		default:
			kept = append(kept, args[index])
		}
	}
	return kept
}

// Calls is each run's arguments, joined, without the output options.
func (r *Runner) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var found []string
	for _, call := range r.calls {
		found = append(found, strings.Join(trimmed(call.Args), " "))
	}
	return found
}

// Ran reports whether a run started with args.
func (r *Runner) Ran(args string) bool {
	return slices.ContainsFunc(r.Calls(), func(call string) bool { return strings.HasPrefix(call, args) })
}

// LookPath finds az anywhere, as if it were installed.
func LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }
