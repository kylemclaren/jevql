// Package psqlout prints result sets the way psql does: aligned tables,
// expanded records, CSV and JSON. Colour is applied only on a terminal.
package psqlout

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/kylemclaren/jevql/internal/canon"
)

// Format selects the output style.
type Format int

const (
	Aligned Format = iota
	CSV
	JSON
)

// Cell is one value with its display text.
type Cell struct {
	Null  bool
	Text  string
	Value any  // typed value for JSON output and sorting
	Right bool // right-align (numbers)
}

// Table is a result set.
type Table struct {
	Columns []string
	Rows    [][]Cell
}

// Options control rendering.
type Options struct {
	Format   Format
	Expanded bool
	Color    bool
	NullText string // aligned only; psql default is empty
}

var (
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	nullStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	countStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// Write renders t.
func Write(w io.Writer, t *Table, o Options) error {
	switch o.Format {
	case CSV:
		return writeCSV(w, t)
	case JSON:
		return writeJSON(w, t)
	}
	if o.Expanded {
		return writeExpanded(w, t, o)
	}
	return writeAligned(w, t, o)
}

func width(s string) int { return utf8.RuneCountInString(s) }

func cellText(c Cell, o Options) string {
	if c.Null {
		return o.NullText
	}
	return c.Text
}

func pad(s string, w int, right bool) string {
	n := w - width(s)
	if n <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", n) + s
	}
	return s + strings.Repeat(" ", n)
}

func center(s string, w int) string {
	n := w - width(s)
	if n <= 0 {
		return s
	}
	left := n / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", n-left)
}

func writeAligned(w io.Writer, t *Table, o Options) error {
	nc := len(t.Columns)
	widths := make([]int, nc)
	for i, c := range t.Columns {
		widths[i] = width(c)
	}
	for _, r := range t.Rows {
		for i := 0; i < nc && i < len(r); i++ {
			// Multi-line values: measure the longest line.
			for _, line := range strings.Split(cellText(r[i], o), "\n") {
				if lw := width(line); lw > widths[i] {
					widths[i] = lw
				}
			}
		}
	}
	style := func(st lipgloss.Style, s string) string {
		if !o.Color {
			return s
		}
		return st.Render(s)
	}
	sep := style(dimStyle, "|")
	var b strings.Builder
	if nc > 0 {
		parts := make([]string, nc)
		for i, c := range t.Columns {
			parts[i] = style(headerStyle, center(c, widths[i]))
		}
		b.WriteString(" " + strings.Join(parts, " "+sep+" ") + "\n")
		dash := make([]string, nc)
		for i := range widths {
			dash[i] = strings.Repeat("-", widths[i]+2)
		}
		b.WriteString(style(dimStyle, strings.Join(dash, "+")) + "\n")
	}
	for _, r := range t.Rows {
		// Split multi-line cells into extra physical lines like psql does.
		lines := 1
		split := make([][]string, nc)
		for i := 0; i < nc; i++ {
			txt := ""
			if i < len(r) {
				txt = cellText(r[i], o)
			}
			split[i] = strings.Split(txt, "\n")
			if len(split[i]) > lines {
				lines = len(split[i])
			}
		}
		for ln := 0; ln < lines; ln++ {
			parts := make([]string, nc)
			for i := 0; i < nc; i++ {
				s := ""
				if ln < len(split[i]) {
					s = split[i][ln]
				}
				right := i < len(r) && r[i].Right
				cellStr := pad(s, widths[i], right)
				if i < len(r) && r[i].Null && o.Color && o.NullText != "" {
					cellStr = style(nullStyle, cellStr)
				}
				if i == nc-1 {
					cellStr = strings.TrimRight(cellStr, " ")
					if right {
						cellStr = pad(s, widths[i], true)
					}
				}
				parts[i] = cellStr
			}
			line := " " + strings.Join(parts, " "+sep+" ")
			if lines > 1 && ln < lines-1 {
				line += " +"
			}
			b.WriteString(strings.TrimRight(line, " ") + "\n")
		}
	}
	n := len(t.Rows)
	unit := "rows"
	if n == 1 {
		unit = "row"
	}
	b.WriteString(style(countStyle, fmt.Sprintf("(%d %s)", n, unit)) + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeExpanded(w io.Writer, t *Table, o Options) error {
	style := func(st lipgloss.Style, s string) string {
		if !o.Color {
			return s
		}
		return st.Render(s)
	}
	cw := 0
	for _, c := range t.Columns {
		if width(c) > cw {
			cw = width(c)
		}
	}
	var b strings.Builder
	for i, r := range t.Rows {
		hdr := fmt.Sprintf("-[ RECORD %d ]", i+1)
		b.WriteString(style(dimStyle, hdr+strings.Repeat("-", max(0, cw+4-width(hdr)))) + "\n")
		for j, c := range t.Columns {
			txt := ""
			if j < len(r) {
				txt = cellText(r[j], o)
			}
			b.WriteString(style(headerStyle, pad(c, cw, false)) + " " + style(dimStyle, "|") + " " + txt + "\n")
		}
	}
	if len(t.Rows) == 0 {
		b.WriteString(style(countStyle, "(0 rows)") + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func writeCSV(w io.Writer, t *Table) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(t.Columns); err != nil {
		return err
	}
	for _, r := range t.Rows {
		rec := make([]string, len(t.Columns))
		for i := range rec {
			if i < len(r) && !r[i].Null {
				rec[i] = r[i].Text
			}
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeJSON(w io.Writer, t *Table) error {
	out := make([]canon.Object, len(t.Rows))
	for i, r := range t.Rows {
		obj := canon.Object{}
		for j, c := range t.Columns {
			if j >= len(r) || r[j].Null {
				obj[c] = nil
				continue
			}
			if r[j].Value != nil {
				obj[c] = r[j].Value
			} else {
				obj[c] = r[j].Text
			}
		}
		out[i] = obj
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// CommandTag prints a psql-style tag for statements without rows.
func CommandTag(w io.Writer, tag string) {
	fmt.Fprintln(w, tag)
}
