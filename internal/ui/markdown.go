package ui

import (
	"fmt"
	"os"

	"github.com/charmbracelet/glamour"
)

func RenderMarkdown(md string) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	if err != nil {
		// Fallback: print raw
		fmt.Fprintln(os.Stderr, md)
		return
	}

	out, err := renderer.Render(md)
	if err != nil {
		fmt.Fprintln(os.Stderr, md)
		return
	}

	fmt.Fprint(os.Stderr, out)
}
