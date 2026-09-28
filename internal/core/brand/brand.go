// Package brand says who this tool is, in one place: its command, the prefix of its
// environment variables, its config directory, and how it introduces itself. Every name
// the running tool shows or reads comes from here, so a copy under another name changes
// this file alone.
package brand

import (
	_ "embed"
	"regexp"
	"runtime/debug"
	"strings"
)

// pseudoVersion is the version Go gives an untagged commit: a time and the commit's hash,
// with +dirty for a changed tree.
var pseudoVersion = regexp.MustCompile(`[0-9]{14}-[0-9a-f]{12}(\+.*)?$`)

// developmentVersion is the version of a build that was not given one.
const developmentVersion = "0.1.0-dev"

// buildVersion is the release, set at build time, as the release builds are, with
// -ldflags "-X github.com/libre-devops/ldo-go-cli/internal/core/brand.buildVersion=0.1.0".
// The linker can only set a string with no initial value of its own, so Version, which is
// worked out, cannot be set that way itself.
var buildVersion string

// Version is the release: the one set at build time, else the module's own version when go
// install fetched a tagged one, else the development version.
var Version = versionOf(buildVersion, debug.ReadBuildInfo)

// versionOf is built, unless it is empty; then the module's tagged version when the build
// knows one (go install ...@v0.1.0 records it); else the development version.
func versionOf(built string, read func() (*debug.BuildInfo, bool)) string {
	if built != "" {
		return built
	}
	info, ok := read()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" || pseudoVersion.MatchString(info.Main.Version) {
		// No version, or a pseudo-version for an untagged commit: nothing better to say.
		return developmentVersion
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

const (
	// DisplayName is how the tool introduces itself.
	DisplayName = "Libre DevOps Helpers"
	// Command is what people type: ldo-go, beside the Python ldo, which it mirrors.
	Command = "ldo-go"
	// EnvPrefix starts every environment variable the tool reads: LDO_CONFIG and the rest,
	// the Python ldo's own, so one setting serves both.
	EnvPrefix = "LDO"
	// ConfigDir is the folder the config file and the kept sign-ins live in: the Python
	// ldo's, so both read the same profiles.
	ConfigDir = "ldo"
	// Repository is where the code and its docs are.
	Repository = "https://github.com/libre-devops/ldo-go-cli"
	// Accent is the colour of an -o html page's header, as #RRGGBB.
	Accent = "#1E3A8A"
)

// Banner is the welcome art, drawn after the Libre DevOps unicorn: banner.txt, plain
// ASCII so it renders in any terminal and font.
//
//go:embed banner.txt
var Banner string

// EnvVar is the environment variable name under this tool's prefix: EnvVar("CONFIG") is
// LDO_CONFIG.
func EnvVar(name string) string { return EnvPrefix + "_" + name }

// Suggest is a command line to suggest to the person, quoted: Suggest("config init") is
// 'ldo-go config init'.
func Suggest(text string) string { return "'" + Command + " " + text + "'" }

// Docs is the web address of a page in docs/, for a hint: Docs("configuration").
func Docs(page string) string { return Repository + "/blob/main/docs/" + page + ".md" }

// The environment variables every command reads.
var (
	ConfigEnv    = EnvVar("CONFIG")
	ProfileEnv   = EnvVar("PROFILE")
	LogFormatEnv = EnvVar("LOG_FORMAT")
	LogLevelEnv  = EnvVar("LOG_LEVEL")
)
