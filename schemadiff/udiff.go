package schemadiff

import (
	"fmt"
	"strings"
)

type opcode struct {
	tag            byte // 'e' equal, 'r' replace, 'd' delete, 'i' insert
	i1, i2, j1, j2 int
}

// opcodes turns a into b using a longest common subsequence of lines (CREATE TABLE statements are short).
func opcodes(a, b []string) []opcode {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
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
	var ops []opcode
	add := func(tag byte, i1, i2, j1, j2 int) {
		if k := len(ops) - 1; k >= 0 && ops[k].tag == tag {
			ops[k].i2, ops[k].j2 = i2, j2
			return
		}
		ops = append(ops, opcode{tag, i1, i2, j1, j2})
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			add('e', i, i+1, j, j+1)
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			add('i', i, i, j, j+1)
			j++
		default:
			add('d', i, i+1, j, j)
			i++
		}
	}
	// A delete next to an insert is a replace.
	var merged []opcode
	for _, op := range ops {
		if k := len(merged) - 1; k >= 0 && op.tag != 'e' && merged[k].tag != 'e' {
			merged[k] = opcode{'r', merged[k].i1, op.i2, merged[k].j1, op.j2}
			continue
		}
		merged = append(merged, op)
	}
	return merged
}

func groupedOpcodes(codes []opcode, n int) [][]opcode {
	if len(codes) == 0 {
		codes = []opcode{{'e', 0, 1, 0, 1}}
	}
	if c := codes[0]; c.tag == 'e' {
		codes[0] = opcode{'e', max(c.i1, c.i2-n), c.i2, max(c.j1, c.j2-n), c.j2}
	}
	if c := codes[len(codes)-1]; c.tag == 'e' {
		codes[len(codes)-1] = opcode{'e', c.i1, min(c.i2, c.i1+n), c.j1, min(c.j2, c.j1+n)}
	}
	var groups [][]opcode
	var g []opcode
	for _, c := range codes {
		if c.tag == 'e' && c.i2-c.i1 > 2*n {
			g = append(g, opcode{'e', c.i1, min(c.i2, c.i1+n), c.j1, min(c.j2, c.j1+n)})
			groups = append(groups, g)
			g = nil
			c.i1, c.j1 = max(c.i1, c.i2-n), max(c.j1, c.j2-n)
		}
		g = append(g, c)
	}
	if len(g) > 0 && !(len(g) == 1 && g[0].tag == 'e') {
		groups = append(groups, g)
	}
	return groups
}

func unifiedRange(start, stop int) string {
	begin, length := start+1, stop-start
	if length == 1 {
		return fmt.Sprint(begin)
	}
	if length == 0 {
		begin--
	}
	return fmt.Sprintf("%d,%d", begin, length)
}

// UnifiedDiff is a unified diff of two lists of lines with n lines of context, like Python's difflib.unified_diff.
// It returns "" when they are equal.
func UnifiedDiff(a, b []string, fromName, toName string, n int) string {
	var out []string
	for gi, g := range groupedOpcodes(opcodes(a, b), n) {
		if gi == 0 {
			out = append(out, "--- "+fromName, "+++ "+toName)
		}
		first, last := g[0], g[len(g)-1]
		out = append(out, fmt.Sprintf("@@ -%s +%s @@", unifiedRange(first.i1, last.i2), unifiedRange(first.j1, last.j2)))
		for _, c := range g {
			if c.tag == 'e' {
				for _, l := range a[c.i1:c.i2] {
					out = append(out, " "+l)
				}
				continue
			}
			if c.tag == 'r' || c.tag == 'd' {
				for _, l := range a[c.i1:c.i2] {
					out = append(out, "-"+l)
				}
			}
			if c.tag == 'r' || c.tag == 'i' {
				for _, l := range b[c.j1:c.j2] {
					out = append(out, "+"+l)
				}
			}
		}
	}
	return strings.Join(out, "\n")
}
