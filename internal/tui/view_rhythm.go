package tui

import "strings"

const workbenchPrimaryTopOffset = 4

func renderWorkbenchRhythm(intro []string, primary []string, tail []string) string {
	lines := make([]string, 0, len(intro)+len(primary)+len(tail)+4)
	lines = append(lines, intro...)
	for len(lines) < workbenchPrimaryTopOffset {
		lines = append(lines, "")
	}
	lines = append(lines, primary...)
	if len(tail) > 0 {
		lines = append(lines, "")
		lines = append(lines, tail...)
	}
	return strings.Join(lines, "\n")
}
