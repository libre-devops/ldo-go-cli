package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/colour"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/logging"
	"github.com/libre-devops/ldo-go-cli/internal/core/render"
)

// Exit codes, the same for every command, so scripts can branch on them.
const (
	ExitOK = 0
	// ExitError is an error, or a token check that failed.
	ExitError = 1
	// ExitUsage is a usage error: a bad option or argument.
	ExitUsage = 2
	// ExitAttention is a command that ran, but found something that needs attention: a
	// device missing or not in the expected state, a secret close to expiry.
	ExitAttention = 3
	// ExitInterrupted is Ctrl-C.
	ExitInterrupted = 130
)

// Attention is the error a command returns when it ran and found something that needs
// attention: the run exits 3, with nothing more to say.
var Attention = &ExitStatus{Code: ExitAttention}

// ExitStatus ends a run with Code, and nothing printed.
type ExitStatus struct{ Code int }

func (e *ExitStatus) Error() string { return fmt.Sprintf("exit %d", e.Code) }

// UsageError is a bad option or argument: exit 2, with the command's name in the hint.
type UsageError struct {
	Message string
	Option  string
}

func (e *UsageError) Error() string {
	if e.Option != "" {
		return "invalid value for " + e.Option + ": " + e.Message
	}
	return e.Message
}

// Usagef is a UsageError about option.
func Usagef(option, format string, args ...any) error {
	return &UsageError{Message: fmt.Sprintf(format, args...), Option: option}
}

type rootFlags struct {
	config    string
	verbose   int
	logFormat string
	logLevel  string
	colour    bool
	noColour  bool
	version   bool
}

// NewRoot is the ldo-go command, with every group registered, for rt.
func NewRoot(rt *Runtime) *cobra.Command {
	// Commands are listed in the order they are added, most used first, not alphabetically.
	cobra.EnableCommandSorting = false
	flags := &rootFlags{}
	root := &cobra.Command{
		Use:   brand.Command,
		Short: brand.DisplayName + ": fast, read-only helpers for Entra ID, Defender XDR, Intune, Azure, Graph, PIM, Logic Apps, ServiceNow, Jira and Confluence, and for Terraform modules. Signs in as you.",
		Long: brand.DisplayName + ": fast, read-only helpers for Entra ID, Defender XDR, Intune, Azure, Graph, PIM, " +
			"Logic Apps, ServiceNow, Jira and Confluence, and for Terraform modules. Signs in as you.\n\nDocs: " + brand.Docs("README"),
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return flags.apply(rt)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.version {
				rt.Console.Println(brand.Command + " " + brand.Version)
				return nil
			}
			showBanner(rt, false)
			return cmd.Help()
		},
	}
	root.SetOut(rt.Console.Out)
	root.SetErr(rt.Console.Err)
	persistent := root.PersistentFlags()
	persistent.StringVar(&flags.config, "config", "", "Config file. Default: $"+brand.ConfigEnv+", else ~/.config/"+brand.ConfigDir+"/config.toml.")
	persistent.CountVarP(&flags.verbose, "verbose", "v", "-v info, -vv debug (stderr).")
	persistent.StringVar(&flags.logFormat, "log-format", "", "Log line format on stderr: text, json, otlp (OTLP/JSON). Default: $"+brand.LogFormatEnv+", else text.")
	persistent.StringVar(&flags.logLevel, "log-level", "", "Minimum log level (trace, debug, info, warn, error, fatal). -v and -vv win.")
	persistent.BoolVar(&flags.colour, "colour", false, "Colour the output. Default: on a terminal, unless NO_COLOR is set; FORCE_COLOR turns it on.")
	persistent.BoolVar(&flags.noColour, "no-colour", false, "Never colour the output.")
	persistent.BoolVar(&flags.colour, "color", false, "The same as --colour.")
	persistent.BoolVar(&flags.noColour, "no-color", false, "The same as --no-colour.")
	_ = persistent.MarkHidden("color")
	_ = persistent.MarkHidden("no-color")
	root.Flags().BoolVar(&flags.version, "version", false, "Show the version and exit.")
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return &UsageError{Message: err.Error()}
	})
	root.AddCommand(commandGroups(rt)...)
	return root
}

// commandGroups are the top-level commands, in the order --help lists them.
func commandGroups(rt *Runtime) []*cobra.Command {
	return []*cobra.Command{
		welcomeCommand(rt), configCommand(rt), profilesCommand(rt), azCommand(rt),
		entraCommand(rt), graphCommand(rt), xdrCommand(rt), intuneCommand(rt),
		azureCommand(rt), keyvaultCommand(rt), logsCommand(rt), logicappCommand(rt), pimCommand(rt), devicesCommand(rt),
		snowCommand(rt), jiraCommand(rt), confluenceCommand(rt),
		newsCommand(rt), plannerCommand(rt), terraformCommand(rt),
		jsonCommand(rt), networkCommand(rt), selfTestCommand(rt),
	}
}

func (f *rootFlags) apply(rt *Runtime) error {
	if f.config != "" {
		rt.ConfigPath = f.config
	}
	switch {
	case f.noColour:
		off := false
		colour.Use(&off)
	case f.colour:
		on := true
		colour.Use(&on)
	default:
		colour.Use(nil)
	}
	format := f.logFormat
	if format == "" {
		format = rt.Env(brand.LogFormatEnv)
	}
	level := f.logLevel
	if level == "" {
		level = rt.Env(brand.LogLevelEnv)
	}
	logger, structured, err := logging.Configure(rt.Console.Err, f.verbose, format, level)
	if err != nil {
		return &UsageError{Message: err.Error(), Option: "--log-format"}
	}
	rt.Console.Structured, rt.Console.Logger = structured, logger
	return nil
}

// Execute runs args as a command line on rt, and is the exit code.
func Execute(rt *Runtime, args []string) int {
	code, _ := execute(rt, args)
	return code
}

// execute runs a command line, and is its exit code and the error it ended with.
func execute(rt *Runtime, args []string) (int, error) {
	root := NewRoot(rt)
	root.SetArgs(args)
	rt.Console.Report = &render.Report{Command: shellJoin(append([]string{brand.Command}, args...)), Heading: commandName(root, args)}
	err := root.ExecuteContext(rt.Ctx())
	if finishErr := finishReport(rt); err == nil {
		err = finishErr
	}
	return exitCode(rt, root, args, err), err
}

func exitCode(rt *Runtime, root *cobra.Command, args []string, err error) int {
	if err == nil {
		return ExitOK
	}
	var exit *ExitStatus
	if errors.As(err, &exit) {
		return exit.Code
	}
	if errors.Is(err, context.Canceled) {
		rt.Console.Error("interrupted", "")
		return ExitInterrupted
	}
	if found := errs.As(err); found != nil {
		rt.Console.Error(found.Message, found.Hint)
		return ExitError
	}
	var usage *UsageError
	if errors.As(err, &usage) || isCobraUsage(err) {
		name := commandName(root, args)
		rt.Console.Error(err.Error(), "see '"+strings.TrimSpace(brand.Command+" "+name)+" --help'")
		return ExitUsage
	}
	rt.Console.Error(err.Error(), "")
	return ExitError
}

func isCobraUsage(err error) bool {
	text := err.Error()
	for _, prefix := range []string{"unknown command", "unknown flag", "unknown shorthand", "accepts ",
		"requires at least", "requires at most", "invalid argument", "flag needs an argument", "required flag"} {
		if strings.Contains(text, prefix) {
			return true
		}
	}
	return false
}

// commandName is the command args run, as "xdr machines": the leading words that name
// one, past the root's options.
func commandName(root *cobra.Command, args []string) string {
	found, _, err := root.Find(args)
	if err != nil || found == root {
		return ""
	}
	path := strings.TrimPrefix(found.CommandPath(), root.Name())
	return strings.TrimSpace(path)
}

func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\n'\"$`\\|&;<>()*?[]#~") {
			quoted[index] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		} else {
			quoted[index] = arg
		}
	}
	return strings.Join(quoted, " ")
}

// showBanner is the welcome banner on stderr: only on a terminal, and never when
// LDO_NO_BANNER is set, unless force.
func showBanner(rt *Runtime, force bool) {
	if rt.Console.Structured && !force {
		return
	}
	if !force && (!colour.IsTerminal(rt.Console.ErrFile) || rt.Env(brand.EnvVar("NO_BANNER")) != "") {
		return
	}
	coloured := rt.Console.ColourErr()
	for row, line := range strings.Split(strings.Trim(brand.Banner, "\n"), "\n") {
		if coloured {
			line = colour.Diagonal(line, row)
		}
		fmt.Fprintln(rt.Console.Err, line)
	}
	footer := brand.DisplayName + "  " + brand.Command + " " + brand.Version
	if coloured {
		footer = colour.Style(footer, nil, false, true)
	}
	fmt.Fprintln(rt.Console.Err, footer)
	fmt.Fprintln(rt.Console.Err)
}

// finishReport writes the page -o html gathered, if it gathered anything.
func finishReport(rt *Runtime) error {
	report := rt.Console.Report
	if report == nil || len(report.Tables) == 0 {
		return nil
	}
	return writeHTML(rt, report)
}
