package ui

import (
	"os/exec"
	"runtime"
)

// Notify sends an OS-level notification. Fails silently if unavailable.
func Notify(title, message string) {
	if runtime.GOOS != "darwin" {
		return
	}
	script := `display notification "` + escapeAppleScript(message) + `" with title "` + escapeAppleScript(title) + `"`
	_ = exec.Command("osascript", "-e", script).Run()
}

func escapeAppleScript(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}
