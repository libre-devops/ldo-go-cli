package render

import (
	"os"

	"golang.org/x/term"
)

// terminalWidth is how many columns wide the terminal behind file is, or 0.
func terminalWidth(file *os.File) int {
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil {
		return 0
	}
	return width
}
