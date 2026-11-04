// Package notify sends macOS notifications.
package notify

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
)

// Notifier sends macOS notifications. When the process runs inside an app
// bundle it posts them natively through UNUserNotificationCenter, so they
// appear as this app and clicking one opens the issue in the browser.
// Outside a bundle (the CLI) it shells out to terminal-notifier (clicking
// opens the issue) or, failing that, osascript, which ships with macOS but
// has no click action.
//
// For notifications that stay on screen until cleared, set the app's style
// to "Alerts" in System Settings > Notifications.
type Notifier struct {
	native           bool
	terminalNotifier string
}

func New() *Notifier {
	if nativeAvailable() {
		nativeInit()
		log.Printf("using native Notification Center notifications")
		return &Notifier{native: true}
	}
	tn, _ := exec.LookPath("terminal-notifier")
	if tn != "" {
		log.Printf("using terminal-notifier (%s)", tn)
	} else {
		log.Printf("terminal-notifier not found (brew install terminal-notifier); falling back to osascript")
	}
	return &Notifier{terminalNotifier: tn}
}

func (n *Notifier) Notify(title, subtitle, message, url string) error {
	if n.native {
		nativeNotify(title, subtitle, message, url)
		return nil
	}

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
