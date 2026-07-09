package ifaces

import (
	"fmt"
	"strconv"
	"strings"
)

// diffContext is the number of unchanged lines of context kept around each
// change in a rendered unified diff, matching the conventional `diff -u`
// default.
const diffContext = 3

// splitLines splits s into lines, each retaining its own trailing line
// terminator (if any) so a diff of CRLF or no-final-newline content stays
// faithful to the source bytes. A trailing empty element (produced when s
// ends in "\n") is dropped.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

type editKind int

const (
	editEqual editKind = iota
	editDelete
	editInsert
)

type editOp struct {
	line string
	kind editKind
}

// diffLines computes a minimal (LCS-based) line-level edit script turning a
// into b, via a plain O(len(a)*len(b)) dynamic program. interfaces(5) files
// are small (tens to low hundreds of lines), so this trades asymptotic
// elegance for an implementation simple enough to audit for the
// safety-critical diff review screen — no third-party diff dependency
// needed (docs/development.md: prefer stdlib).
func diffLines(a, b []string) []editOp {
	n, m := len(a), len(b)
	dp := make([][]int32, n+1)
	for i := range dp {
		dp[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case a[i] == b[j]:
				dp[i][j] = dp[i+1][j+1] + 1
			case dp[i+1][j] >= dp[i][j+1]:
				dp[i][j] = dp[i+1][j]
			default:
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ops := make([]editOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, editOp{kind: editEqual, line: a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, editOp{kind: editDelete, line: a[i]})
			i++
		default:
			ops = append(ops, editOp{kind: editInsert, line: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, editOp{kind: editDelete, line: a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, editOp{kind: editInsert, line: b[j]})
	}
	return ops
}

type hunk struct {
	ops      []editOp
	oldStart int
	oldCount int
	newStart int
	newCount int
}

func (h hunk) header() string {
	return fmt.Sprintf("@@ -%s +%s @@\n", rangeStr(h.oldStart, h.oldCount), rangeStr(h.newStart, h.newCount))
}

func rangeStr(start, count int) string {
	if count == 1 {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// buildHunks groups an edit script into unified-diff hunks, padding each
// change with up to context lines of surrounding equal content and merging
// hunks whose padded ranges touch or overlap.
func buildHunks(ops []editOp, context int) []hunk {
	n := len(ops)
	oldPos := make([]int, n+1)
	newPos := make([]int, n+1)
	for i, op := range ops {
		oldPos[i+1], newPos[i+1] = oldPos[i], newPos[i]
		switch op.kind {
		case editEqual:
			oldPos[i+1]++
			newPos[i+1]++
		case editDelete:
			oldPos[i+1]++
		case editInsert:
			newPos[i+1]++
		}
	}

	var changeIdx []int
	for i, op := range ops {
		if op.kind != editEqual {
			changeIdx = append(changeIdx, i)
		}
	}
	if len(changeIdx) == 0 {
		return nil
	}

	// Group raw change positions where the equal-line gap between
	// consecutive changes is small enough that their padded context
	// windows will touch.
	type span struct{ lo, hi int } // inclusive change-index range
	var rawSpans []span
	curLo, curHi := changeIdx[0], changeIdx[0]
	for _, idx := range changeIdx[1:] {
		gap := idx - curHi - 1
		if gap <= 2*context {
			curHi = idx
		} else {
			rawSpans = append(rawSpans, span{curLo, curHi})
			curLo, curHi = idx, idx
		}
	}
	rawSpans = append(rawSpans, span{curLo, curHi})

	// Pad each span by context ops on both sides, then merge overlapping
	// padded ranges.
	type rng struct{ lo, hi int } // ops index range [lo,hi)
	var ranges []rng
	for _, s := range rawSpans {
		lo := s.lo - context
		if lo < 0 {
			lo = 0
		}
		hi := s.hi + context + 1
		if hi > n {
			hi = n
		}
		if len(ranges) > 0 && lo <= ranges[len(ranges)-1].hi {
			if hi > ranges[len(ranges)-1].hi {
				ranges[len(ranges)-1].hi = hi
			}
			continue
		}
		ranges = append(ranges, rng{lo, hi})
	}

	hunks := make([]hunk, 0, len(ranges))
	for _, r := range ranges {
		oc := oldPos[r.hi] - oldPos[r.lo]
		nc := newPos[r.hi] - newPos[r.lo]
		oldStart, newStart := oldPos[r.lo]+1, newPos[r.lo]+1
		if oc == 0 {
			oldStart = oldPos[r.lo]
		}
		if nc == 0 {
			newStart = newPos[r.lo]
		}
		hunks = append(hunks, hunk{
			oldStart: oldStart, oldCount: oc,
			newStart: newStart, newCount: nc,
			ops: ops[r.lo:r.hi],
		})
	}
	return hunks
}

func ensureNL(s string) string {
	if strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

// UnifiedDiff renders a standard unified diff (as `diff -u oldPath newPath`
// would, with diffContext lines of context) between oldContent and
// newContent. Returns "" when the two are byte-identical, so callers can
// use that to decide whether a file belongs in a diff response at all
// (docs/api.md's diff endpoint only lists files the changeset touches).
func UnifiedDiff(oldPath, newPath, oldContent, newContent string) string {
	if oldContent == newContent {
		return ""
	}
	a, b := splitLines(oldContent), splitLines(newContent)
	hunks := buildHunks(diffLines(a, b), diffContext)
	if len(hunks) == 0 {
		return ""
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", oldPath)
	fmt.Fprintf(&sb, "+++ %s\n", newPath)
	for _, h := range hunks {
		sb.WriteString(h.header())
		for _, op := range h.ops {
			var prefix byte
			switch op.kind {
			case editEqual:
				prefix = ' '
			case editDelete:
				prefix = '-'
			case editInsert:
				prefix = '+'
			}
			sb.WriteByte(prefix)
			sb.WriteString(ensureNL(op.line))
		}
	}
	return sb.String()
}
