package gitcli

import (
	"fmt"
	"strings"
)

// UnifiedDiff returns a unified diff of a and b with 3 context lines
// ("" when equal). existed and exists say whether the file existed
// before and exists now (/dev/null headers).
func UnifiedDiff(path string, a, b []byte, existed, exists bool) string {
	oldL, newL := SplitLines(a), SplitLines(b)
	changes := DiffLines(oldL, newL)
	if len(changes) == 0 {
		return ""
	}
	var w strings.Builder
	from, to := "a/"+path, "b/"+path
	if !existed {
		from = "/dev/null"
	}
	if !exists {
		to = "/dev/null"
	}
	fmt.Fprintf(&w, "--- %s\n+++ %s\n", from, to)
	const ctx = 3
	line := func(prefix, s string) {
		w.WriteString(prefix + strings.TrimSuffix(s, "\n") + "\n")
		if !strings.HasSuffix(s, "\n") {
			w.WriteString("\\ No newline at end of file\n")
		}
	}
	for i := 0; i < len(changes); {
		// Group changes whose context overlaps into one hunk.
		j := i
		for j+1 < len(changes) && changes[j+1].OldStart-changes[j].OldEnd <= 2*ctx {
			j++
		}
		os := max(0, changes[i].OldStart-ctx)
		oe := min(len(oldL), changes[j].OldEnd+ctx)
		ns := changes[i].NewStart - (changes[i].OldStart - os)
		ne := changes[j].NewEnd + (oe - changes[j].OldEnd)
		fmt.Fprintf(&w, "@@ -%s +%s @@\n", unifiedRange(os, oe-os), unifiedRange(ns, ne-ns))
		pos := os
		for k := i; k <= j; k++ {
			c := changes[k]
			for ; pos < c.OldStart; pos++ {
				line(" ", oldL[pos])
			}
			for p := c.OldStart; p < c.OldEnd; p++ {
				line("-", oldL[p])
			}
			for p := c.NewStart; p < c.NewEnd; p++ {
				line("+", newL[p])
			}
			pos = c.OldEnd
		}
		for ; pos < oe; pos++ {
			line(" ", oldL[pos])
		}
		i = j + 1
	}
	return w.String()
}

func unifiedRange(start, n int) string {
	if n == 0 {
		return fmt.Sprintf("%d,0", start)
	}
	if n == 1 {
		return fmt.Sprintf("%d", start+1)
	}
	return fmt.Sprintf("%d,%d", start+1, n)
}
