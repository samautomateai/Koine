package tools

import (
	"fmt"
	"strings"
)

const diffContext = 3

// maxDiffCells bounds the line-diff table; a changed region larger than this is reported as one replaced block.
const maxDiffCells = 4_000_000

type diffOp struct {
	kind byte // ' ' kept, '-' removed, '+' added
	text string
}

// UnifiedDiff compares two texts line by line and returns a unified diff (3 lines of context) together with the 1-based line, in the new text,
// of the first change. The diff is empty when the texts are equal.
func UnifiedDiff(oldText, newText, path string) (string, int) {
	if oldText == newText {
		return "", 0
	}
	ops := diffLines(strings.Split(oldText, "\n"), strings.Split(newText, "\n"))
	first := 0
	line := 0
	for _, op := range ops {
		if op.kind != '-' {
			line++
		}
		if op.kind != ' ' && first == 0 {
			first = line
			if op.kind == '-' {
				first = line + 1
			}
		}
	}
	return "--- " + path + "\n+++ " + path + "\n" + hunks(ops), first
}

// diffLines is a line diff: common prefix and suffix are cut off, and the middle is aligned with a longest-common-subsequence table.
func diffLines(a, b []string) []diffOp {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ma, mb := a[pre:len(a)-suf], b[pre:len(b)-suf]
	var ops []diffOp
	for _, l := range a[:pre] {
		ops = append(ops, diffOp{' ', l})
	}
	ops = append(ops, lcsDiff(ma, mb)...)
	for _, l := range a[len(a)-suf:] {
		ops = append(ops, diffOp{' ', l})
	}
	return ops
}

func lcsDiff(a, b []string) []diffOp {
	var ops []diffOp
	if len(a)*len(b) > maxDiffCells {
		for _, l := range a {
			ops = append(ops, diffOp{'-', l})
		}
		for _, l := range b {
			ops = append(ops, diffOp{'+', l})
		}
		return ops
	}
	// t[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
	t := make([][]int, len(a)+1)
	for i := range t {
		t[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{' ', a[i]})
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			ops = append(ops, diffOp{'-', a[i]})
			i++
		default:
			ops = append(ops, diffOp{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, diffOp{'-', a[i]})
	}
	for ; j < len(b); j++ {
		ops = append(ops, diffOp{'+', b[j]})
	}
	return ops
}

// hunks groups the changed operations with diffContext lines around them into "@@" blocks.
func hunks(ops []diffOp) string {
	var changed []int
	for i, op := range ops {
		if op.kind != ' ' {
			changed = append(changed, i)
		}
	}
	var b strings.Builder
	for k := 0; k < len(changed); {
		lo := max(changed[k]-diffContext, 0)
		hi := changed[k]
		for k < len(changed) && changed[k]-hi <= 2*diffContext+1 {
			hi = changed[k]
			k++
		}
		hi = min(hi+diffContext, len(ops)-1)
		oldStart, newStart := 1, 1
		for _, op := range ops[:lo] {
			if op.kind != '+' {
				oldStart++
			}
			if op.kind != '-' {
				newStart++
			}
		}
		oldN, newN := 0, 0
		var body strings.Builder
		for _, op := range ops[lo : hi+1] {
			if op.kind != '+' {
				oldN++
			}
			if op.kind != '-' {
				newN++
			}
			body.WriteString(string(op.kind) + op.text + "\n")
		}
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n%s", oldStart, oldN, newStart, newN, body.String())
	}
	return b.String()
}
