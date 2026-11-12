package diff

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// contextLines is how many unchanged lines are kept around each change.
const contextLines = 3

// Edit is one change to a description: the text before and after, as GitHub
// recorded it.
type Edit struct {
	When   time.Time
	Editor string // login, or "" when unknown
	Old    string
	New    string
	// Known is false when GitHub no longer has the text of this revision
	// (its edit history was deleted), so there is nothing to compare.
	Known bool
}

// Change describes edits to an issue's description, newest first.
type Change struct {
	Key   string // owner/repo#123
	Title string
	URL   string // the issue's web URL
	Edits []Edit
}

// HTML renders the change as a standalone page. Everything that comes from
// GitHub is escaped, and a CSP forbids scripts and network access.
func HTML(c Change) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'">
<meta name="viewport" content="width=device-width, initial-scale=1">
`)
	fmt.Fprintf(&b, "<title>Description changed: %s</title>\n", html.EscapeString(c.Key))
	b.WriteString("<style>" + pageCSS + "</style></head><body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", html.EscapeString(c.Title))
	fmt.Fprintf(&b, `<p class="meta">%s · description changed`, html.EscapeString(c.Key))
	if strings.HasPrefix(c.URL, "https://") {
		fmt.Fprintf(&b, ` · <a href="%s" rel="noreferrer">Open on GitHub</a>`, html.EscapeString(c.URL))
	}
	b.WriteString("</p>\n")

	for _, e := range c.Edits {
		writeEdit(&b, e, len(c.Edits) > 1)
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

func writeEdit(b *strings.Builder, e Edit, multiple bool) {
	b.WriteString("<section>\n")
	who := "Edited"
	if e.Editor != "" {
		who = "Edited by " + e.Editor
	}
	when := ""
	if !e.When.IsZero() {
		when = " · " + e.When.Local().Format("Jan 2, 2006 15:04")
	}
	tag := "h2"
	if !multiple {
		tag = "h3"
	}
	fmt.Fprintf(b, "<%s>%s%s</%s>\n", tag, html.EscapeString(who), html.EscapeString(when), tag)

	if !e.Known {
		b.WriteString(`<p class="note">GitHub no longer has the text of this revision (its edit history was deleted), so there is nothing to compare.</p>` + "\n</section>\n")
		return
	}
	rows := Compare(e.Old, e.New)
	added, removed := Stats(rows)
	fmt.Fprintf(b, `<p class="stats"><span class="add">+%d</span> <span class="del">−%d</span> lines</p>`+"\n", added, removed)
	if added == 0 && removed == 0 {
		b.WriteString(`<p class="note">Only whitespace or line endings changed.</p>` + "\n")
	}

	show := visibleRows(rows)
	b.WriteString("<table>\n")
	for i := 0; i < len(rows); i++ {
		if !show[i] {
			skipped := 0
			for i < len(rows) && !show[i] {
				skipped++
				i++
			}
			fmt.Fprintf(b, `<tr class="gap"><td colspan="3">⋯ %d unchanged line%s</td></tr>`+"\n", skipped, plural(skipped))
			i--
			continue
		}
		writeRow(b, rows[i])
	}
	b.WriteString("</table>\n</section>\n")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// visibleRows marks the rows to show: every change plus contextLines of
// unchanged lines around it.
func visibleRows(rows []Row) []bool {
	show := make([]bool, len(rows))
	for i, r := range rows {
		if r.Kind == Equal {
			continue
		}
		for j := i - contextLines; j <= i+contextLines; j++ {
			if j >= 0 && j < len(rows) {
				show[j] = true
			}
		}
	}
	return show
}

func writeRow(b *strings.Builder, r Row) {
	class, sign := "eq", " "
	switch r.Kind {
	case Insert:
		class, sign = "ins", "+"
	case Delete:
		class, sign = "del", "−"
	}
	fmt.Fprintf(b, `<tr class="%s"><td class="no">%s</td><td class="no">%s</td><td class="code"><span class="sign">%s</span>`,
		class, lineNo(r.OldNo), lineNo(r.NewNo), sign)
	for _, s := range r.Spans {
		text := html.EscapeString(s.Text)
		if s.Changed {
			fmt.Fprintf(b, "<mark>%s</mark>", text)
		} else {
			b.WriteString(text)
		}
	}
	b.WriteString("</td></tr>\n")
}

func lineNo(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

const pageCSS = `
:root{--bg:#fff;--fg:#1f2328;--muted:#656d76;--line:#d0d7de;--add:#dafbe1;--addmark:#aceebb;--del:#ffebe9;--delmark:#ffc1ba;--gap:#f6f8fa}
@media (prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8d96a0;--line:#30363d;--add:#12261e;--addmark:#1f6f3a;--del:#2d1618;--delmark:#8e2a2f;--gap:#161b22}}
body{margin:0;padding:24px 16px 48px;background:var(--bg);color:var(--fg);font:15px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;max-width:980px;margin-inline:auto}
h1{font-size:20px;margin:0 0 4px}
h2,h3{font-size:15px;margin:24px 0 6px}h3{margin-top:8px}
.meta{color:var(--muted);margin:0 0 12px}.meta a{color:inherit}
.stats{margin:0 0 12px;font-weight:600}.stats .add{color:#1a7f37}.stats .del{color:#cf222e}
.note{background:var(--gap);border:1px solid var(--line);border-radius:6px;padding:8px 12px}
table{width:100%;border-collapse:collapse;border:1px solid var(--line);font:13px/1.5 ui-monospace,SFMono-Regular,Menlo,monospace}
td{padding:0 8px;vertical-align:top}
td.no{width:1%;min-width:36px;text-align:right;color:var(--muted);user-select:none;white-space:nowrap}
td.code{white-space:pre-wrap;word-break:break-word}
.sign{display:inline-block;width:1.4em;color:var(--muted);user-select:none}
tr.ins{background:var(--add)}tr.del{background:var(--del)}
tr.ins mark{background:var(--addmark);color:inherit}tr.del mark{background:var(--delmark);color:inherit}
tr.gap td{background:var(--gap);color:var(--muted);text-align:center;padding:2px 0}
pre.plain{white-space:pre-wrap;word-break:break-word;border:1px solid var(--line);border-radius:6px;padding:12px}
`

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// WriteFile renders c into dir and returns the file's path. The directory and
// file are private to the user because descriptions can come from private
// repositories.
func WriteFile(dir string, c Change) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-%d.html", unsafeName.ReplaceAllString(c.Key, "_"), time.Now().UnixNano())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(HTML(c)), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// Prune deletes diff pages in dir older than maxAge.
func Prune(dir string, maxAge time.Duration, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > maxAge {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// DefaultDir returns where diff pages are written.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "github-spyglass-diffs")
	}
	return filepath.Join(home, ".github-spyglass", "diffs")
}
