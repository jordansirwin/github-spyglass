package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func text(r Row) string {
	var b strings.Builder
	for _, s := range r.Spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

func changedWords(r Row) []string {
	var out []string
	for _, s := range r.Spans {
		if s.Changed {
			out = append(out, strings.TrimSpace(s.Text))
		}
	}
	return out
}

func TestCompareLines(t *testing.T) {
	rows := Compare("one\ntwo\nthree\n", "one\n2\nthree\nfour\n")
	var got []string
	for _, r := range rows {
		sign := map[Kind]string{Equal: " ", Delete: "-", Insert: "+"}[r.Kind]
		got = append(got, sign+text(r))
	}
	want := []string{" one", "-two", "+2", " three", "+four"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows = %q, want %q", got, want)
	}
	added, removed := Stats(rows)
	if added != 2 || removed != 1 {
		t.Errorf("stats = +%d -%d, want +2 -1", added, removed)
	}
}

func TestCompareLineNumbers(t *testing.T) {
	rows := Compare("a\nb\nc", "a\nc\nd")
	// a(1,1) b-(2,0) c(3,2) d+(0,3)
	want := [][2]int{{1, 1}, {2, 0}, {3, 2}, {0, 3}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d", len(rows), len(want))
	}
	for i, r := range rows {
		if r.OldNo != want[i][0] || r.NewNo != want[i][1] {
			t.Errorf("row %d numbers = (%d,%d), want %v", i, r.OldNo, r.NewNo, want[i])
		}
	}
}

func TestCompareMarksChangedWords(t *testing.T) {
	rows := Compare("the quick brown fox", "the slow brown fox")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if got := changedWords(rows[0]); len(got) != 1 || got[0] != "quick" {
		t.Errorf("deleted words = %q, want [quick]", got)
	}
	if got := changedWords(rows[1]); len(got) != 1 || got[0] != "slow" {
		t.Errorf("inserted words = %q, want [slow]", got)
	}
	if text(rows[0]) != "the quick brown fox" || text(rows[1]) != "the slow brown fox" {
		t.Errorf("spans must reproduce the lines: %q / %q", text(rows[0]), text(rows[1]))
	}
}

func TestCompareEmptyAndIdentical(t *testing.T) {
	if rows := Compare("", ""); len(rows) != 0 {
		t.Errorf("empty vs empty: %d rows", len(rows))
	}
	rows := Compare("", "new text")
	if len(rows) != 1 || rows[0].Kind != Insert {
		t.Errorf("empty -> text: %+v", rows)
	}
	rows = Compare("old", "")
	if len(rows) != 1 || rows[0].Kind != Delete {
		t.Errorf("text -> empty: %+v", rows)
	}
	if a, r := Stats(Compare("same\ntext", "same\r\ntext")); a != 0 || r != 0 {
		t.Errorf("CRLF vs LF should be identical, got +%d -%d", a, r)
	}
}

func TestHugeInputFallsBackWithoutBlowingUp(t *testing.T) {
	var a, b []string
	for i := 0; i < 3000; i++ {
		a = append(a, "old line "+string(rune('a'+i%26))+strings.Repeat("x", i%7))
		b = append(b, "new line "+string(rune('a'+i%26))+strings.Repeat("y", i%5))
	}
	rows := Compare(strings.Join(a, "\n"), strings.Join(b, "\n"))
	added, removed := Stats(rows)
	if added != 3000 || removed != 3000 {
		t.Errorf("stats = +%d -%d, want +3000 -3000", added, removed)
	}
}

func single(old, cur string) Change {
	return Change{Key: "o/r#1", Title: "t", URL: "https://github.com/o/r/issues/1",
		Edits: []Edit{{When: time.Now(), Editor: "alice", Old: old, New: cur, Known: true}}}
}

func TestHTMLEscapesUntrustedContent(t *testing.T) {
	c := single("safe", `<img src=x onerror=alert(1)>`)
	c.Title = `<script>alert("t")</script>`
	c.URL = `https://github.com/o/r/issues/1"><script>x</script>`
	c.Edits[0].Editor = `<b>eve</b>`
	page := HTML(c)
	for _, bad := range []string{"<script>", "<img src=x", "<b>eve</b>"} {
		if strings.Contains(page, bad) {
			t.Errorf("page contains unescaped %q", bad)
		}
	}
	if !strings.Contains(page, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("changed text should appear escaped")
	}
	if !strings.Contains(page, "Content-Security-Policy") {
		t.Errorf("page should carry a CSP")
	}
}

func TestHTMLOmitsNonHTTPSLinks(t *testing.T) {
	c := single("a", "b")
	c.URL = "javascript:alert(1)"
	if strings.Contains(HTML(c), "javascript:") {
		t.Error("non-https URL must not become a link")
	}
}

func TestHTMLCollapsesUnchangedRuns(t *testing.T) {
	var old, cur []string
	for i := 0; i < 30; i++ {
		line := "line " + strings.Repeat("z", i+1)
		old = append(old, line)
		cur = append(cur, line)
	}
	cur[15] = "CHANGED"
	page := HTML(single(strings.Join(old, "\n"), strings.Join(cur, "\n")))
	if !strings.Contains(page, "unchanged lines") {
		t.Error("long unchanged runs should be collapsed")
	}
	if strings.Contains(page, "line "+strings.Repeat("z", 1)+"</td>") {
		t.Error("distant context lines should be hidden")
	}
	if !strings.Contains(page, "CHANGED") {
		t.Error("the change itself must be shown")
	}
}

func TestHTMLShowsEachEditWithEditorAndTime(t *testing.T) {
	c := Change{Key: "o/r#1", Title: "t", Edits: []Edit{
		{When: time.Date(2026, 11, 12, 14, 5, 0, 0, time.Local), Editor: "bob", Old: "two", New: "three", Known: true},
		{When: time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local), Editor: "alice", Old: "one", New: "two", Known: true},
	}}
	page := HTML(c)
	for _, want := range []string{"Edited by bob", "Edited by alice", "Nov 12, 2026 14:05", "Nov 12, 2026 09:00"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Index(page, "Edited by bob") > strings.Index(page, "Edited by alice") {
		t.Error("edits should be listed newest first, as given")
	}
}

func TestHTMLDeletedRevision(t *testing.T) {
	c := Change{Key: "o/r#1", Title: "t", Edits: []Edit{{Editor: "bob", Known: false}}}
	page := HTML(c)
	if !strings.Contains(page, "no longer has the text") {
		t.Errorf("page should explain the missing revision text")
	}
}

func TestWriteFileAndPrune(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "diffs")
	now := time.Now()
	c := single("a", "b")
	c.Key = "owner/repo#12"
	path, err := WriteFile(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(filepath.Base(path), "/#") {
		t.Errorf("file name should be sanitized: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}

	Prune(dir, time.Hour, now) // fresh file stays
	if _, err := os.Stat(path); err != nil {
		t.Error("fresh diff was pruned")
	}
	Prune(dir, time.Hour, now.Add(2*time.Hour)) // old file goes
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("old diff should have been pruned")
	}
}
