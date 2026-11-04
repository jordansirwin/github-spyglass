// github-spyglass-app is the menu bar version of github-spyglass: it watches
// GitHub issues matching a search filter, sends macOS notifications on
// changes, and shows recent updates in a menu bar dropdown.
package main

import (
	_ "embed"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"

	"github.com/jirwin/github-spyglass/internal/config"
	"github.com/jirwin/github-spyglass/internal/github"
	"github.com/jirwin/github-spyglass/internal/notify"
	"github.com/jirwin/github-spyglass/internal/state"
	"github.com/jirwin/github-spyglass/internal/watcher"
)

//go:embed icon.png
var iconPNG []byte

// maxRecent is how many updates the dropdown shows.
const maxRecent = 10

type event struct {
	when  time.Time
	label string
	url   string
}

type app struct {
	cfgPath   string
	statePath string

	mu     sync.Mutex
	cfg    *config.Config
	st     *state.State
	events []event // newest first
	paused bool
	seed   bool // next poll seeds state without notifying

	watch    *watcher.Watcher
	ticker   *time.Ticker
	checkNow chan struct{}
	quit     chan struct{}

	status    *systray.MenuItem
	lastCheck *systray.MenuItem
	empty     *systray.MenuItem
	slots     [maxRecent]*systray.MenuItem
	pause     *systray.MenuItem
}

func main() {
	setupLogging()

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	statePath := state.DefaultPath()
	st, firstRun, err := state.Load(statePath)
	if err != nil {
		log.Fatalf("loading state: %v", err)
	}

	a := &app{
		cfgPath:   cfgPath,
		statePath: statePath,
		cfg:       cfg,
		st:        st,
		seed:      firstRun,
		checkNow:  make(chan struct{}, 1),
		quit:      make(chan struct{}),
	}
	a.watch = &watcher.Watcher{
		Client:   github.New(github.ResolveToken(cfg.Token)),
		Notifier: &recordingNotifier{app: a, inner: notify.New()},
	}

	systray.Run(a.onReady, a.onExit)
}

func setupLogging() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".github-spyglass")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "app.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
}

func (a *app) onReady() {
	systray.SetIcon(iconPNG)
	systray.SetTooltip("GitHub Spyglass")

	a.status = systray.AddMenuItem("Starting…", "")
	a.status.Disable()
	a.lastCheck = systray.AddMenuItem("Last check: never", "")
	a.lastCheck.Disable()
	systray.AddSeparator()

	a.empty = systray.AddMenuItem("No updates yet", "")
	a.empty.Disable()
	for i := range a.slots {
		a.slots[i] = systray.AddMenuItem("", "Open in browser")
		a.slots[i].Hide()
		go a.slotClickLoop(i)
	}
	systray.AddSeparator()

	check := systray.AddMenuItem("Check Now", "")
	a.pause = systray.AddMenuItem("Pause", "")
	systray.AddSeparator()

	filter := systray.AddMenuItem("Edit Filter…", "")
	login := systray.AddMenuItem("Start at Login", "")
	if bundlePath() == "" {
		login.Disable() // not running from a .app bundle
	}
	systray.AddSeparator()

	quit := systray.AddMenuItem("Quit GitHub Spyglass", "")

	go func() {
		for {
			select {
			case <-check.ClickedCh:
				a.triggerCheck()
			case <-a.pause.ClickedCh:
				a.togglePause()
			case <-filter.ClickedCh:
				a.editFilter()
			case <-login.ClickedCh:
				a.toggleLogin(login)
			case <-quit.ClickedCh:
				systray.Quit()
				return
			}
		}
	}()

	go a.run()

	// First launch with no filter configured: prompt right away.
	if a.cfg.Query == "" {
		a.status.SetTitle("No filter set")
		go a.editFilter()
	}
}

func (a *app) onExit() {
	close(a.quit)
}

func (a *app) run() {
	ctx := context.Background()
	a.poll(ctx)
	a.mu.Lock()
	a.ticker = time.NewTicker(a.cfg.PollInterval())
	a.mu.Unlock()
	for {
		select {
		case <-a.ticker.C:
			a.poll(ctx)
		case <-a.checkNow:
			a.poll(ctx)
		case <-a.quit:
			a.ticker.Stop()
			return
		}
	}
}

func (a *app) poll(ctx context.Context) {
	a.mu.Lock()
	if a.paused || a.cfg.Query == "" {
		a.mu.Unlock()
		return
	}
	a.watch.Query = a.cfg.Query
	seed := a.seed
	a.seed = false
	st := a.st
	a.mu.Unlock()

	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := a.watch.Poll(cctx, st, seed); err != nil {
		log.Printf("poll failed: %v", err)
		a.status.SetTitle("⚠ " + truncate(err.Error(), 70))
		return
	}
	a.status.SetTitle("Watching: " + truncate(a.watch.Query, 50))
	a.lastCheck.SetTitle("Last check: " + time.Now().Format("15:04:05"))
	if err := state.Save(a.statePath, st); err != nil {
		log.Printf("saving state: %v", err)
	}
}

func (a *app) triggerCheck() {
	select {
	case a.checkNow <- struct{}{}:
	default:
	}
}

func (a *app) togglePause() {
	a.mu.Lock()
	a.paused = !a.paused
	paused := a.paused
	a.mu.Unlock()
	if paused {
		a.pause.SetTitle("Resume")
		a.status.SetTitle("Paused")
	} else {
		a.pause.SetTitle("Pause")
		a.triggerCheck()
	}
}

// editFilter prompts for the search filter with an AppleScript dialog.
func (a *app) editFilter() {
	a.mu.Lock()
	current := a.cfg.Query
	a.mu.Unlock()

	script := fmt.Sprintf(
		`text returned of (display dialog "GitHub issue search filter:" default answer %s with title "GitHub Spyglass" buttons {"Cancel", "Save"} default button "Save")`,
		appleScriptString(current))
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return // cancelled
	}
	query := strings.TrimSpace(string(out))
	if query == "" || query == current {
		return
	}

	a.mu.Lock()
	a.cfg.Query = query
	a.seed = true // don't notify for everything the new filter matches
	if err := a.cfg.Save(a.cfgPath); err != nil {
		log.Printf("saving config: %v", err)
	}
	if a.ticker != nil {
		a.ticker.Reset(a.cfg.PollInterval())
	}
	a.mu.Unlock()

	log.Printf("filter changed to %q", query)
	a.triggerCheck()
}

func (a *app) toggleLogin(item *systray.MenuItem) {
	bundle := bundlePath()
	if bundle == "" {
		return
	}
	if item.Checked() {
		script := `tell application "System Events" to delete login item "GitHub Spyglass"`
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			log.Printf("removing login item: %v", err)
			return
		}
		item.Uncheck()
	} else {
		script := fmt.Sprintf(
			`tell application "System Events" to make login item at end with properties {path:%s, hidden:false}`,
			appleScriptString(bundle))
		if err := exec.Command("osascript", "-e", script).Run(); err != nil {
			log.Printf("adding login item: %v", err)
			return
		}
		item.Check()
	}
}

func (a *app) slotClickLoop(i int) {
	for range a.slots[i].ClickedCh {
		a.mu.Lock()
		var url string
		if i < len(a.events) {
			url = a.events[i].url
		}
		a.mu.Unlock()
		if url != "" {
			if err := exec.Command("open", url).Run(); err != nil {
				log.Printf("opening %s: %v", url, err)
			}
		}
	}
}

func (a *app) addEvent(title, message, url string) {
	label := truncate(title+" · "+message, 90)
	a.mu.Lock()
	a.events = append([]event{{when: time.Now(), label: label, url: url}}, a.events...)
	if len(a.events) > maxRecent {
		a.events = a.events[:maxRecent]
	}
	events := a.events
	a.mu.Unlock()

	for i, slot := range a.slots {
		if i < len(events) {
			slot.SetTitle(events[i].when.Format("15:04") + "  " + events[i].label)
			slot.Show()
		} else {
			slot.Hide()
		}
	}
	a.empty.Hide()
}

// recordingNotifier feeds every notification into the dropdown's recent
// updates list before delivering it.
type recordingNotifier struct {
	app   *app
	inner watcher.Notifier
}

func (r *recordingNotifier) Notify(title, subtitle, message, url string) error {
	r.app.addEvent(title, message, url)
	return r.inner.Notify(title, subtitle, message, url)
}

// bundlePath returns the containing .app bundle, or "" when running outside
// one (e.g. go run).
func bundlePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	// <bundle>.app/Contents/MacOS/<exe>
	dir := filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	if strings.HasSuffix(dir, ".app") {
		return dir
	}
	return ""
}

func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
