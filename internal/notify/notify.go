// Package notify sends macOS notifications.
package notify

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// Notifier sends macOS notifications via terminal-notifier (clicking a
// notification opens the issue in the browser), falling back to osascript,
// which ships with macOS but has no click action.
//
// For notifications that stay on screen until cleared, set terminal-notifier's
// style to "Alerts" in System Settings > Notifications > terminal-notifier.
type Notifier struct {
	terminalNotifier string
}

func New() *Notifier {
	tn, _ := exec.LookPath("terminal-notifier")
	if tn != "" {
		log.Printf("using terminal-notifier (%s)", tn)
	} else {
		log.Printf("terminal-notifier not found (brew install terminal-notifier); falling back to osascript")
	}
	return &Notifier{terminalNotifier: tn}
}

func (n *Notifier) Notify(title, subtitle, message, url string) error {
	if n.terminalNotifier != "" {
		args := []string{"-title", title, "-message", message, "-sound", "default"}
		if subtitle != "" {
			args = append(args, "-subtitle", subtitle)
		}
		if url != "" {
			args = append(args, "-open", url)
		}
		return exec.Command(n.terminalNotifier, args...).Run()
	}

	script := fmt.Sprintf("display notification %s with title %s subtitle %s sound name \"default\"",
		appleScriptString(message), appleScriptString(title), appleScriptString(subtitle))
	return exec.Command("osascript", "-e", script).Run()
}

func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
