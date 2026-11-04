// github-spyglass-app is the menu bar version of github-spyglass: it watches
// GitHub issues matching a search filter, sends macOS notifications on
// changes, and shows recent updates in a menu bar dropdown.
package main

import (
	"context"
	_ "embed"
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

// recentPresets are the dropdown sizes offered in the menu; the default
// (no config) is 10 via config.RecentCount.
var recentPresets = []int{5, 10, 25, 50}

type event struct {
	when  time.Time
	label string
	url   string
}

type app struct {
	cfgPath   string
	statePath string
	logPath   string

	// notifier delivers directly, bypassing the recent-updates recording
	// (used by the debug test notification).
	notifier watcher.Notifier

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

	status         *systray.MenuItem
	lastCheck      *systray.MenuItem
	empty          *systray.MenuItem
	slots          [config.MaxRecent]*systray.MenuItem
	pause          *systray.MenuItem
	intervalParent *systray.MenuItem
	intervalItems  []*systray.MenuItem
	recentParent   *systray.MenuItem
	recentItems    []*systray.MenuItem
}

// intervalPresets are the poll intervals offered in the menu; the default
// (no config) is 5m via config.PollInterval.
var intervalPresets = []time.Duration{
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	time.Hour,
}

func main() {
	logPath := setupLogging()

	// Fire one notification and exit: for testing delivery, permission, and
	// icon without waiting for a real issue update. Run the binary inside
	// the installed bundle so it notifies as the app:
	//   "~/Applications/GitHub Spyglass.app/Contents/MacOS/github-spyglass-app" -test-notification
	if len(os.Args) > 1 && os.Args[1] == "-test-notification" {
		n := notify.New()
		if err := n.Notify("GitHub Spyglass", "Test", "Notification test — click to open GitHub", "https://github.com"); err != nil {
			log.Fatalf("test notification: %v", err)
		}
		time.Sleep(2 * time.Second) // let the async delivery finish
		return
	}

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
		logPath:   logPath,
		notifier:  notify.New(),
		cfg:       cfg,
		st:        st,
		seed:      firstRun,
		checkNow:  make(chan struct{}, 1),
		quit:      make(chan struct{}),
	}
	a.watch = &watcher.Watcher{
		Client:   github.New(github.ResolveToken(cfg.Token)),
		Notifier: &recordingNotifier{app: a, inner: a.notifier},
	}

	systray.Run(a.onReady, a.onExit)
}

// setupLogging mirrors logs to ~/.github-spyglass/app.log and returns that
// path, or "" when the log file couldn't be set up.
func setupLogging() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(home, ".github-spyglass")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "app.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return path
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

	cur := a.cfg.PollInterval()
	a.intervalParent = systray.AddMenuItem("Poll Interval: "+formatDuration(cur), "")
	for _, d := range intervalPresets {
		item := a.intervalParent.AddSubMenuItemCheckbox(formatDuration(d), "", d == cur)
		a.intervalItems = append(a.intervalItems, item)
		go func(d time.Duration, item *systray.MenuItem) {
			for range item.ClickedCh {
				a.setInterval(d)
			}
		}(d, item)
	}

	curRecent := a.cfg.RecentCount()
	a.recentParent = systray.AddMenuItem(fmt.Sprintf("Recent Updates: %d", curRecent), "")
	for _, n := range recentPresets {
		item := a.recentParent.AddSubMenuItemCheckbox(fmt.Sprintf("%d", n), "", n == curRecent)
		a.recentItems = append(a.recentItems, item)
		go func(n int, item *systray.MenuItem) {
			for range item.ClickedCh {
				a.setRecent(n)
			}
		}(n, item)
	}

	login := systray.AddMenuItem("Start at Login", "")
	if bundlePath() == "" {
		login.Disable() // not running from a .app bundle
	}

	debug := systray.AddMenuItem("Debug", "")
	testNotif := debug.AddSubMenuItem("Send Test Notification", "")
	openLog := debug.AddSubMenuItem("Open Log", "")
	if a.logPath == "" {
		openLog.Disable()
	}
	openConfig := debug.AddSubMenuItem("Open Config File", "")
	openState := debug.AddSubMenuItem("Open State File", "")
	openFolder := debug.AddSubMenuItem("Open Data Folder", "")
	notifSettings := debug.AddSubMenuItem("Notification Settings…", "")
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
			case <-testNotif.ClickedCh:
				a.sendTestNotification()
			case <-openLog.ClickedCh:
				openPath(a.logPath)
			case <-openConfig.ClickedCh:
				openTextFile(a.cfgPath)
			case <-openState.ClickedCh:
				openTextFile(a.statePath)
			case <-openFolder.ClickedCh:
				openPath(filepath.Dir(a.cfgPath))
			case <-notifSettings.ClickedCh:
				openPath("x-apple.systempreferences:com.apple.Notifications-Settings.extension")
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

// setInterval switches the poll interval, persists it, and restarts the
// poll timer.
func (a *app) setInterval(d time.Duration) {
	a.mu.Lock()
	a.cfg.Interval = formatDuration(d)
	if err := a.cfg.Save(a.cfgPath); err != nil {
		log.Printf("saving config: %v", err)
	}
	if a.ticker != nil {
		a.ticker.Reset(d)
	}
	a.mu.Unlock()

	a.intervalParent.SetTitle("Poll Interval: " + formatDuration(d))
	for i, item := range a.intervalItems {
		if intervalPresets[i] == d {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
	log.Printf("poll interval set to %s", formatDuration(d))
}

func formatDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	return fmt.Sprintf("%dm", d/time.Minute)
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

// sendTestNotification fires one notification directly (not recorded in the
// recent-updates list): for checking delivery, permission, and the icon.
func (a *app) sendTestNotification() {
	log.Printf("sending test notification")
	if err := a.notifier.Notify("GitHub Spyglass", "Test",
		"Notification test — click to open GitHub", "https://github.com"); err != nil {
		log.Printf("test notification: %v", err)
	}
}

// openPath opens a file, folder, or URL with its default handler.
func openPath(path string) {
	if path == "" {
		return
	}
	if err := exec.Command("open", path).Run(); err != nil {
		log.Printf("opening %s: %v", path, err)
	}
}

// openTextFile opens a file in the default text editor, which beats the
// default handler for extensionless or .json files.
func openTextFile(path string) {
	if _, err := os.Stat(path); err != nil {
		log.Printf("opening %s: %v", path, err)
		return
	}
	if err := exec.Command("open", "-t", path).Run(); err != nil {
		log.Printf("opening %s: %v", path, err)
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
	if len(a.events) > config.MaxRecent {
		a.events = a.events[:config.MaxRecent]
	}
	events := a.visibleEventsLocked()
	a.mu.Unlock()

	a.renderEvents(events)
}

// visibleEventsLocked returns the events the dropdown should show under the
// configured limit. Callers must hold a.mu.
func (a *app) visibleEventsLocked() []event {
	if n := a.cfg.RecentCount(); len(a.events) > n {
		return a.events[:n]
	}
	return a.events
}

// renderEvents fills the dropdown's event slots, hiding the unused ones.
func (a *app) renderEvents(events []event) {
	for i, slot := range a.slots {
		if i < len(events) {
			slot.SetTitle(events[i].when.Format("15:04") + "  " + events[i].label)
			slot.Show()
		} else {
			slot.Hide()
		}
	}
	if len(events) == 0 {
		a.empty.Show()
	} else {
		a.empty.Hide()
	}
}

// setRecent switches how many recent updates the dropdown shows and
// persists it.
func (a *app) setRecent(n int) {
	a.mu.Lock()
	a.cfg.Recent = n
	if err := a.cfg.Save(a.cfgPath); err != nil {
		log.Printf("saving config: %v", err)
	}
	events := a.visibleEventsLocked()
	a.mu.Unlock()

	a.recentParent.SetTitle(fmt.Sprintf("Recent Updates: %d", n))
	for i, item := range a.recentItems {
		if recentPresets[i] == n {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
	a.renderEvents(events)
	log.Printf("recent updates set to %d", n)
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
