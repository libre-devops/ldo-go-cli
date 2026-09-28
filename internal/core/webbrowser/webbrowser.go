// Package webbrowser says whether a browser can be opened here, decided much as the Azure
// CLI decides it, and opens one without letting a failure stop a sign-in.
package webbrowser

import (
	"log/slog"
	"runtime"

	"github.com/pkg/browser"
)

// CanLaunch is false on a Linux machine with no browser to open (a headless server, say).
// Windows and macOS always have one. On Linux, BROWSER counts, as does a desktop session
// with xdg-open, and WSL with wslview.
func CanLaunch(getenv func(string) string, lookPath func(string) (string, error)) bool {
	return canLaunch(runtime.GOOS, getenv, lookPath)
}

func canLaunch(system string, getenv func(string) string, lookPath func(string) (string, error)) bool {
	if system != "linux" {
		return true
	}
	if getenv("BROWSER") != "" {
		return true
	}
	onPath := func(name string) bool {
		_, err := lookPath(name)
		return err == nil
	}
	if (getenv("DISPLAY") != "" || getenv("WAYLAND_DISPLAY") != "") && onPath("xdg-open") {
		return true
	}
	return getenv("WSL_DISTRO_NAME") != "" && onPath("wslview")
}

// Open opens link in a browser, if it can. The link has been shown already, so a browser
// that will not start only costs the person a click, and is only logged.
func Open(link string) error {
	if err := browser.OpenURL(link); err != nil {
		slog.Debug("could not open a browser", "error", err.Error())
	}
	return nil
}
