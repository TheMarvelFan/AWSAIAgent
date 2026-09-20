package configdiff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Unified renders a unified-style text diff of the two documents pretty-printed
// as JSON, with `context` unchanged lines around each hunk.
func Unified(from, to json.RawMessage, context int) (string, error) {
	aLines, err := prettyLines(from)
	if err != nil {
		return "", err
	}
	bLines, err := prettyLines(to)
	if err != nil {
		return "", err
	}
	if len(aLines) > maxTextDiffLines || len(bLines) > maxTextDiffLines {
		return "", ErrTooLargeForText
	}
	if context < 0 {
		context = 0
	}
	return render(aLines, bLines, lcs(aLines, bLines), context), nil
}

func prettyLines(raw json.RawMessage) ([]string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []string{}, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	// Marshaling through the generic form sorts map keys, so an identical
	// document always renders identically regardless of field order upstream.
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return strings.Split(string(buf), "\n"), nil
}

// lcs builds the classic longest-common-subsequence table. n*m is bounded by
// maxTextDiffLines above.
func lcs(a, b []string) [][]int {
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i, v := range slices.Backward(a) {
		for j := len(b) - 1; j >= 0; j-- {
			if v == b[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	return table
}

type diffLine struct {
	kind byte // ' ', '-', '+'
	text string
}

func render(a, b []string, table [][]int, context int) string {
	var lines []diffLine
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			lines = append(lines, diffLine{' ', a[i]})
			i, j = i+1, j+1
		case table[i+1][j] >= table[i][j+1]:
			lines = append(lines, diffLine{'-', a[i]})
			i++
		default:
			lines = append(lines, diffLine{'+', b[j]})
			j++
		}
	}
	for ; i < len(a); i++ {
		lines = append(lines, diffLine{'-', a[i]})
	}
	for ; j < len(b); j++ {
		lines = append(lines, diffLine{'+', b[j]})
	}

	// Keep only lines within `context` of a change.
	keep := make([]bool, len(lines))
	for idx, l := range lines {
		if l.kind == ' ' {
			continue
		}
		lo := max(0, idx-context)
		hi := min(len(lines)-1, idx+context)
		for k := lo; k <= hi; k++ {
			keep[k] = true
		}
	}

	var sb strings.Builder
	gap := false
	for idx, l := range lines {
		if !keep[idx] {
			gap = true
			continue
		}
		if gap && sb.Len() > 0 {
			sb.WriteString("@@\n")
		}
		gap = false
		_, err := fmt.Fprintf(&sb, "%c%s\n", l.kind, l.text)
		if err != nil {
			return ""
		}
	}
	return sb.String()
}
