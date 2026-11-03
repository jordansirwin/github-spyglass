# github-spyglass

Watches GitHub issues matching a [search filter](https://docs.github.com/en/search-github/searching-on-github/searching-issues-and-pull-requests) and sends macOS notifications when they change — like GitHub notifications, but for an arbitrary filter instead of just things you're subscribed to.

You get notified when a matched issue:

- receives a **new comment** (with the commenter and an excerpt)
- is **closed or reopened**
- is otherwise **edited** (title, body, labels, assignees, ...)
- **newly matches** the filter

## Build

```bash
go build .
```

## Usage

```bash
./github-spyglass -query 'repo:elastic/cloud is:issue is:open label:my-team' -interval 5m
```

The first run seeds state silently; notifications start on the next poll. State lives in `~/.github-spyglass/state.json`.

Flags:

| Flag | Default | Description |
|---|---|---|
| `-query` | (required) | GitHub search filter. Add `is:issue` to exclude PRs — the search API returns both. |
| `-interval` | `5m` | Poll interval (minimum `1m`). |
| `-state` | `~/.github-spyglass/state.json` | State file path. Use different files to run multiple filters at once. |
| `-token` | auto | GitHub token. Defaults to `$GITHUB_TOKEN`, `$GH_TOKEN`, then `gh auth token`. |
| `-once` | false | Single poll and exit (useful for cron/launchd). |
| `-verbose` | false | Verbose logging. |

## Notifications

Uses **[terminal-notifier](https://github.com/julienXX/terminal-notifier)** (`brew install terminal-notifier`): clicking a notification opens the issue in your browser. If it isn't installed, falls back to `osascript` (ships with macOS, no click action).

To make notifications **stick on screen until you clear them**, set the style to Alerts once:

System Settings → Notifications → terminal-notifier → Alert style: **Alerts**

The first notification triggers a macOS prompt to allow notifications from terminal-notifier — click Allow (the terminal-notifier entry only appears in System Settings after that first notification).

## Authentication

A token is strongly recommended: unauthenticated search is limited to 10 requests/minute and can't see private repos. If you use the `gh` CLI, no setup is needed — the token is pulled from `gh auth token`.

## Running in the background

The app loops on its own, so the simplest option is a background shell:

```bash
nohup ./github-spyglass -query '...' >> ~/.github-spyglass/log.txt 2>&1 &
```

For something that survives reboots, create a LaunchAgent at `~/Library/LaunchAgents/com.jirwin.github-spyglass.plist`:

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
    <string>repo:elastic/cloud is:issue is:open label:my-team</string>
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
