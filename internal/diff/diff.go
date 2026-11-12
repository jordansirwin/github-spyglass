// Package diff compares two versions of an issue description and renders the
// result as a standalone HTML page, so a notification can offer to show
// exactly what changed.
package diff

import "strings"

// Kind says how a row differs between the old and new text.
type Kind int

const (
	Equal Kind = iota
	Delete
	Insert
)

// Span is a run of text within a row; Changed marks the words that differ
// from the paired line on the other side.
type Span struct {
	Text    string
	Changed bool
}

// Row is one line of the diff. OldNo and NewNo are 1-based line numbers, or 0
// when the line doesn't exist on that side.
type Row struct {
	Kind  Kind
	OldNo int
	NewNo int
	Spans []Span
}

// maxCells bounds the LCS table; larger comparisons fall back to treating
// the whole text as replaced rather than using unbounded memory.
const maxCells = 4_000_000

type opKind byte

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type op struct {
	kind opKind
	// ai and bi index into the inputs: ai for equal/delete, bi for equal/insert.
	ai, bi int
}

// diffOps returns an edit script turning a into b, built from a longest
// common subsequence.
func diffOps(a, b []string) []op {
	// Trim the common prefix and suffix so the table covers only the middle.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	am, bm := a[pre:len(a)-suf], b[pre:len(b)-suf]

	ops := make([]op, 0, len(a)+len(b))
	for i := 0; i < pre; i++ {
		ops = append(ops, op{opEqual, i, i})
	}
	ops = append(ops, middleOps(am, bm, pre, pre)...)
	for i := 0; i < suf; i++ {
		ops = append(ops, op{opEqual, len(a) - suf + i, len(b) - suf + i})
	}
	return ops
}

func middleOps(a, b []string, aoff, boff int) []op {
	n, m := len(a), len(b)
	var ops []op
	if n == 0 || m == 0 || (n+1)*(m+1) > maxCells {
		for i := 0; i < n; i++ {
			ops = append(ops, op{opDelete, aoff + i, 0})
		}
		for j := 0; j < m; j++ {
			ops = append(ops, op{opInsert, 0, boff + j})
		}
		return ops
	}

	// lcs[i][j] is the LCS length of a[i:] and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{opEqual, aoff + i, boff + j})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{opDelete, aoff + i, 0})
			i++
		default:
			ops = append(ops, op{opInsert, 0, boff + j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{opDelete, aoff + i, 0})
	}
	for ; j < m; j++ {
		ops = append(ops, op{opInsert, 0, boff + j})
	}
	return ops
}

// splitLines splits text into lines, treating CRLF as LF. Empty text has no
// lines at all.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// tokenize splits a line into alternating runs of whitespace and non-space,
// so joining the tokens reproduces the line exactly.
func tokenize(s string) []string {
	var toks []string
	start := 0
	space := false
	for i, r := range s {
		isSpace := r == ' ' || r == '\t'
		if i == 0 {
			space = isSpace
			continue
		}
		if isSpace != space {
			toks = append(toks, s[start:i])
			start = i
			space = isSpace
		}
	}
	if s != "" {
		toks = append(toks, s[start:])
	}
	return toks
}

// wordSpans compares two lines word by word and returns the spans of each
// with the differing words marked.
func wordSpans(old, cur string) (oldSpans, newSpans []Span) {
	a, b := tokenize(old), tokenize(cur)
	for _, o := range diffOps(a, b) {
		switch o.kind {
		case opEqual:
			oldSpans = appendSpan(oldSpans, a[o.ai], false)
			newSpans = appendSpan(newSpans, b[o.bi], false)
		case opDelete:
			oldSpans = appendSpan(oldSpans, a[o.ai], true)
		case opInsert:
			newSpans = appendSpan(newSpans, b[o.bi], true)
		}
	}
	return oldSpans, newSpans
}

func appendSpan(spans []Span, text string, changed bool) []Span {
	if n := len(spans); n > 0 && spans[n-1].Changed == changed {
		spans[n-1].Text += text
		return spans
	}
	return append(spans, Span{Text: text, Changed: changed})
}

// Compare diffs old against cur line by line. Within a block of changed
// lines, the first deleted line is paired with the first inserted one, and so
// on, and the differing words in each pair are marked.
func Compare(old, cur string) []Row {
	a, b := splitLines(old), splitLines(cur)
	ops := diffOps(a, b)

	var rows []Row
	oldNo, newNo := 0, 0
	for i := 0; i < len(ops); {
		if ops[i].kind == opEqual {
			oldNo++
			newNo++
			rows = append(rows, Row{Equal, oldNo, newNo, []Span{{Text: a[ops[i].ai]}}})
			i++
			continue
		}
		// Gather the whole block of consecutive changes.
		var dels, ins []string
		for i < len(ops) && ops[i].kind != opEqual {
			if ops[i].kind == opDelete {
				dels = append(dels, a[ops[i].ai])
			} else {
				ins = append(ins, b[ops[i].bi])
			}
			i++
		}
		delSpans := make([][]Span, len(dels))
		insSpans := make([][]Span, len(ins))
		for k := 0; k < len(dels) && k < len(ins); k++ {
			delSpans[k], insSpans[k] = wordSpans(dels[k], ins[k])
		}
		for k, line := range dels {
			oldNo++
			spans := delSpans[k]
			if spans == nil {
				spans = []Span{{Text: line}}
			}
			rows = append(rows, Row{Delete, oldNo, 0, spans})
		}
		for k, line := range ins {
			newNo++
			spans := insSpans[k]
			if spans == nil {
				spans = []Span{{Text: line}}
			}
			rows = append(rows, Row{Insert, 0, newNo, spans})
		}
	}
	return rows
}

// Stats counts the inserted and deleted lines in rows.
func Stats(rows []Row) (added, removed int) {
	for _, r := range rows {
		switch r.Kind {
		case Insert:
			added++
		case Delete:
			removed++
		}
	}
	return added, removed
}
