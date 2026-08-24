package cli

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// TimeFormat is the timestamp form every table uses: short enough to fit, and
// unambiguous without a locale.
const TimeFormat = "2006-01-02 15:04"

// FieldWidth is the column every detail view aligns its values at, counted from
// the start of the line. `approval show` and `history show` are read one after
// the other often enough that a shared column is worth more than a per-command
// fit.
const FieldWidth = 14

// PathWidth and KeyWidth cap the first column of an indented pair list. A path
// or fingerprint key longer than this is truncated rather than allowed to push
// its value off the terminal.
const (
	PathWidth = 52
	KeyWidth  = 56
)

// SummaryWidth is the narrower column `status` uses. Its labels are one word
// each, and the record views' column would leave them stranded.
const SummaryWidth = 12

// Field renders one labeled line of a detail view, e.g.
//
//	command:   npm run cleanup
//
// The padding is computed rather than written out, because a hand-counted
// `"  reason:    %s"` is correct until someone adds a longer label and only the
// eye notices.
func Field(out io.Writer, label, format string, args ...any) {
	FieldAt(out, FieldWidth, label, format, args...)
}

// FieldAt renders a labeled line at a chosen column, for a view whose labels
// do not belong in the shared one.
func FieldAt(out io.Writer, width int, label, format string, args ...any) {
	fmt.Fprint(out, FieldLabelAt(width, label))
	fmt.Fprintf(out, format, args...)
	fmt.Fprintln(out)
}

// FieldLabel renders the label and its padding, for a line assembled in pieces.
func FieldLabel(label string) string { return FieldLabelAt(FieldWidth, label) }

// FieldLabelAt renders a label padded to a chosen column. A label too wide for
// the column still gets one space, so it never runs into its value.
func FieldLabelAt(width int, label string) string {
	text := "  " + label + ":"
	if padding := width - utf8.RuneCountInString(text); padding > 0 {
		return text + strings.Repeat(" ", padding)
	}
	return text + " "
}

// Summary renders one line of the `status` overview. Its labels carry no colon,
// because they name a quantity rather than a property:
//
//	daemon    running (pid 4711)
//	active    3
func Summary(out io.Writer, label, format string, args ...any) {
	text := "  " + label
	if padding := SummaryWidth - utf8.RuneCountInString(text); padding > 0 {
		text += strings.Repeat(" ", padding)
	} else {
		text += " "
	}
	fmt.Fprint(out, text)
	fmt.Fprintf(out, format, args...)
	fmt.Fprintln(out)
}

// FieldHeading renders a label that introduces indented lines below it, e.g.
//
//	targets:
//	  ./dist    WORKSPACE_GENERATED
func FieldHeading(out io.Writer, label string) {
	fmt.Fprintf(out, "  %s:\n", label)
}

// Pairs is a two-column list of indented values, aligned to the widest key so a
// long one shifts nothing. It is the shape used for targets and their scopes,
// and for fingerprints and their hashes.
type Pairs struct {
	rows [][2]string
	// keyLimit caps the key column; 0 means unbounded.
	keyLimit int
}

// NewPairs starts a list whose keys are truncated to a limit.
func NewPairs(keyLimit int) *Pairs { return &Pairs{keyLimit: keyLimit} }

// Add appends one key and its value.
func (p *Pairs) Add(key, value string) {
	p.rows = append(p.rows, [2]string{Truncate(key, p.keyLimit), value})
}

// Empty reports whether anything was added.
func (p *Pairs) Empty() bool { return len(p.rows) == 0 }

// Render writes the list, indented under a heading.
func (p *Pairs) Render(out io.Writer) {
	width := 0
	for _, row := range p.rows {
		if got := utf8.RuneCountInString(row[0]); got > width {
			width = got
		}
	}
	for _, row := range p.rows {
		if row[1] == "" {
			fmt.Fprintf(out, "    %s\n", row[0])
			continue
		}
		padding := strings.Repeat(" ", width-utf8.RuneCountInString(row[0]))
		fmt.Fprintf(out, "    %s%s  %s\n", row[0], padding, row[1])
	}
}

// Table renders aligned columns, truncating cells that would break the layout.
type Table struct {
	headers []string
	rows    [][]string
	// widths caps a column; 0 means unbounded.
	widths []int
}

// NewTable starts a table with the given headers.
func NewTable(headers ...string) *Table {
	return &Table{headers: headers, widths: make([]int, len(headers))}
}

// WithWidths caps individual columns, in header order. A zero leaves a column
// unbounded.
func (t *Table) WithWidths(widths ...int) *Table {
	copy(t.widths, widths)
	return t
}

// Add appends a row; missing cells are rendered empty.
func (t *Table) Add(cells ...string) {
	row := make([]string, len(t.headers))
	copy(row, cells)
	t.rows = append(t.rows, row)
}

// Empty reports whether the table has no rows.
func (t *Table) Empty() bool { return len(t.rows) == 0 }

// Render writes the table, padding every column to its widest cell. The last
// column is never padded, so trailing whitespace never ends up in output a user
// might copy.
func (t *Table) Render(out io.Writer) {
	if len(t.headers) == 0 {
		return
	}

	cells := make([][]string, 0, len(t.rows)+1)
	cells = append(cells, t.truncateRow(t.headers))
	for _, row := range t.rows {
		cells = append(cells, t.truncateRow(row))
	}

	widths := make([]int, len(t.headers))
	for _, row := range cells {
		for i, cell := range row {
			if width := utf8.RuneCountInString(cell); width > widths[i] {
				widths[i] = width
			}
		}
	}

	for _, row := range cells {
		var line strings.Builder
		for i, cell := range row {
			if i == len(row)-1 {
				line.WriteString(cell)
				break
			}
			line.WriteString(cell)
			line.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+2))
		}
		fmt.Fprintln(out, strings.TrimRight(line.String(), " "))
	}
}

func (t *Table) truncateRow(row []string) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		out[i] = Truncate(cell, t.widths[i])
	}
	return out
}

// Truncate shortens text to a rune budget, marking that it was cut. A limit of
// zero or less leaves the text alone.
func Truncate(text string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}
	if limit <= 1 {
		return "…"
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}

// Plural renders a count with its noun, so a line never reads "1 commands" or
// falls back to the "(s)" that saves the writer and costs the reader.
func Plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// FormatTime renders a timestamp for a table, or "-" when it is unset.
func FormatTime(at *time.Time) string {
	if at == nil || at.IsZero() {
		return "-"
	}
	return at.Local().Format(TimeFormat)
}

// Dash renders an empty value as "-" so columns never look truncated.
func Dash(text string) string {
	if strings.TrimSpace(text) == "" {
		return "-"
	}
	return text
}

// FingerprintShort renders a hash prefix, which is all a user needs to compare
// two of them.
func FingerprintShort(value string) string {
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}
