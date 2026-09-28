// Command ldo-go is the Go port of the Libre DevOps Helpers CLI (ldo): fast, read-only
// helpers for Microsoft, ServiceNow and Atlassian, signing in as you.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/libre-devops/ldo-go-cli/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	rt := cli.NewRuntime()
	rt.Context = ctx
	code := cli.Execute(rt, os.Args[1:])
	stop()
	os.Exit(code)
}
