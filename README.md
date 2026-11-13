<p align="center">
  <img src="cmd/github-spyglass-app/icon.png" alt="GitHub Spyglass icon" width="128">
</p>

# github-spyglass

Watches GitHub issues matching a [search filter](https://docs.github.com/en/search-github/searching-on-github/searching-issues-and-pull-requests) and sends macOS notifications when they change. Think GitHub notifications, but for any filter you can write instead of just the things you're subscribed to.

## Why not just GitHub notifications?

GitHub notifications aren't great at picking out the specific things that matter from everything else going on. What you get is decided by subscriptions and repo-level settings, not by what's actually important to you. Spyglass follows a search instead.

- **Watch anything a search can describe.** A filter like `org:my-org is:issue is:open label:needs-triage` covers matching issues across repos, even ones you never opened, commented on, or subscribed to. New matches show up at the next poll.
- **Says what changed.** Notifications spell it out: "description edited", "labels +bug -triage", "assignees +alice", "milestone set to v2", "renamed to ...", a new comment with an excerpt, closed, reopened.
- **A real diff for description edits.** GitHub keeps an [edit history](https://docs.github.com/en/communities/moderating-comments-and-conversations/tracking-changes-in-a-comment), but you have to open the issue and click "edited", one revision at a time. Spyglass pulls it for you and shows what changed, who changed it, and when.
- **Quiet.** No inbox to triage, no email, no mobile push. Just a macOS notification per change and a short list of recent updates in the menu bar.
- **Local.** It runs on your Mac with your own token. Nothing to host, no account.

It's not a replacement for GitHub notifications. It polls instead of getting pushes, it's built around issues (a filter without `is:issue` also matches PRs, but it won't track reviews or CI), and it only sees what your token can see. Use both: GitHub for the broad picture, Spyglass for the stuff you can't afford to miss.

## What you get notified about

When a matched issue:

- gets a **new comment** (with the commenter and an excerpt)
- is **closed or reopened**
- is **edited** (title, description, labels, assignees, milestone)
- **newly matches** the filter

If the description was edited, the notification has a **View Diff** action. It opens the changes in your browser with the changed words highlighted, covering every edit since the app last looked (up to 10). macOS shows notification actions when you hover over the banner, or set the alert style to **Alerts** in System Settings → Notifications to keep them on screen. Diff pages live in `~/.github-spyglass/diffs/` for 14 days.

## Screenshots

**Menu bar**

<img src="docs/screenshots/github-spyglass-menubar.png" alt="The GitHub Spyglass menu bar" width="420">

**Notification**

<img src="docs/screenshots/github-spyglass-notification.png" alt="A GitHub Spyglass notification" width="420">

## Menu bar app

```bash
make run-app      # build GitHub Spyglass.app, install to ~/Applications, launch
```

The icon shows up in the menu bar (no Dock icon). The menu has:

- the filter being watched and the last check time
- **recent updates**, timestamped. Click one to open the issue. Hold **⌥ (Option)** over one that edited a description to switch it to **View Diff**.
- **Check Now** and **Pause**
- **Settings**:
  - **Edit Filter…** changes the search filter right away (existing matches are recorded silently)
  - **Poll Interval** from 1m to 1h, default 5m
  - **Show Recent Updates** shows 5/10/25/50 (default 10)
  - **Set GitHub Token…** saves a personal access token to `~/.github-spyglass/token`
  - **Start at Login** adds a login item (macOS asks to let it control System Events the first time)
- **Debug**: test notifications, open the log/config/state files or data folder, notification settings

First launch asks for a filter. Everything lives in `~/.github-spyglass/`: `config.json`, `state.json`, `events.json` (recent updates, kept across restarts), `token`, and `app.log`.

Don't run the app and the CLI on the same state file at once. They'll race and double-notify.

## CLI

```bash
make build        # builds ./github-spyglass
make test         # unit tests
make install      # go install into $GOBIN

./github-spyglass -query 'repo:jordansirwin/github-spyglass is:issue is:open label:spyglass' -interval 5m
```

The first run seeds state silently, and notifications start on the next poll.

| Flag | Default | What it does |
|---|---|---|
| `-query` | (required) | GitHub search filter. Add `is:issue` to leave out PRs. |
| `-interval` | `5m` | Poll interval (minimum `1m`). |
| `-state` | `~/.github-spyglass/state.json` | State file. Use different files to run several filters. |
| `-token` | auto | GitHub token (see [Authentication](#authentication)). |
| `-once` | false | Poll once and exit (handy for cron/launchd). |
| `-verbose` | false | Verbose logging. |

The app posts native notifications as GitHub Spyglass, so allow them when macOS asks. The CLI isn't an app bundle, so it uses [terminal-notifier](https://github.com/julienXX/terminal-notifier) (`brew install terminal-notifier`) and falls back to `osascript` (no click action) without it. To make notifications stick until you clear them, set the alert style to **Alerts** under GitHub Spyglass (or terminal-notifier) in System Settings → Notifications.

## Authentication

You want a token: without one, search is limited to 10 requests a minute and can't see private repos. The first of these that's set wins:

1. `token` in `config.json` (app) or `-token` (CLI)
2. the token saved with **Settings → Set GitHub Token…**
3. `$GITHUB_TOKEN` or `$GH_TOKEN`
4. `gh auth token`, if you use the `gh` CLI

Apps launched from Finder or at login don't see your shell's environment, so use **Set GitHub Token…** for the menu bar app (a personal access token with the `repo` scope, authorized for SSO orgs). A stale `gh` keychain login breaks it too, since your shell's `GITHUB_TOKEN` isn't visible to the app.

## Running the CLI in the background

Quick and dirty:

```bash
nohup ./github-spyglass -query '...' >> ~/.github-spyglass/log.txt 2>&1 &
```

To survive reboots, add a LaunchAgent at `~/Library/LaunchAgents/com.jirwin.github-spyglass.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.jirwin.github-spyglass</string>
  <key>ProgramArguments</key>
  <array>
    <string>/path/to/github-spyglass</string>
    <string>-query</string>
    <string>repo:jordansirwin/github-spyglass is:issue is:open label:spyglass</string>
    <string>-interval</string>
    <string>5m</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/github-spyglass.log</string>
  <key>StandardErrorPath</key><string>/tmp/github-spyglass.log</string>
</dict>
</plist>
```

Then `launchctl load ~/Library/LaunchAgents/com.jirwin.github-spyglass.plist`.

## License

[MIT](LICENSE). The vendored [`third_party/systray`](third_party/systray) is a modified copy of [fyne-io/systray](https://github.com/fyne-io/systray) and keeps its own Apache-2.0 license.
