package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The CLI is where a user finds out what Intenter did, and it is read in a
// terminal of unknown width, piped into `grep`, and diffed against yesterday's
// run. That makes layout a correctness property rather than decoration: output
// that shifts between runs cannot be diffed, and a column that a long path
// pushes off the screen hides the very thing being explained.

func TestFieldsAlignAcrossEveryDetailView(t *testing.T) {
	// `approval show` and `history show` are read one after the other, so their
	// values start at the same column.
	var out bytes.Buffer
	Field(&out, "cwd", "/w/demo")
	Field(&out, "created by", "npm run cleanup")
	Field(&out, "used", "%d time(s)", 3)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3:\n%s", len(lines), out.String())
	}
	for _, line := range lines {
		if got := strings.Index(line, strings.TrimLeft(line, " ")[0:1]); got != 2 {
			t.Errorf("line %q starts at column %d, want an indent of 2", line, got)
		}
	}

	for _, line := range lines {
		value := line[FieldWidth:]
		if strings.HasPrefix(value, " ") {
			t.Errorf("line %q is padded past the shared column", line)
		}
		if value == "" {
			t.Errorf("line %q has no value", line)
		}
	}
}

func TestSummaryLinesAlignAtTheirOwnColumn(t *testing.T) {
	// `status` reads as a list of quantities, not a record, so it has a
	// narrower column than the detail views — but one column, not eleven
	// hand-counted paddings.
	var out bytes.Buffer
	Summary(&out, "daemon", "running")
	Summary(&out, "endpoint", "/tmp/x.sock")
	Summary(&out, "asked", "%d", 3)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	for _, line := range lines {
		value := line[SummaryWidth:]
		if strings.HasPrefix(value, " ") || value == "" {
			t.Errorf("line %q does not start its value at the shared column", line)
		}
	}
	if SummaryWidth >= FieldWidth {
		t.Errorf("the summary column (%d) should be narrower than the record column (%d)",
			SummaryWidth, FieldWidth)
	}
}

func TestAFieldLabelTooLongForTheColumnStillSeparates(t *testing.T) {
	// A label wider than the column must not run into its value.
	var out bytes.Buffer
	Field(&out, strings.Repeat("x", FieldWidth+10), "value")

	line := strings.TrimRight(out.String(), "\n")
	if !strings.HasSuffix(line, ": value") {
		t.Errorf("line = %q, want a space between the label and its value", line)
	}
}

func TestPairsAlignToTheWidestKey(t *testing.T) {
	pairs := NewPairs(0)
	pairs.Add("./dist", "WORKSPACE_GENERATED")
	pairs.Add("./a-much-longer-path/inside", "WORKSPACE")
	pairs.Add("~/x", "HOME")

	var out bytes.Buffer
	pairs.Render(&out)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(lines))
	}

	columns := make([]int, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("line %q does not split into a key and a value", line)
		}
		columns = append(columns, strings.Index(line, fields[1]))
	}
	for i, column := range columns {
		if column != columns[0] {
			t.Errorf("line %d puts its value at column %d, want %d — a long key must not "+
				"shift the others:\n%s", i, column, columns[0], out.String())
		}
	}
}

func TestPairsTruncateAKeyRatherThanPushItsValueOffTheScreen(t *testing.T) {
	pairs := NewPairs(20)
	pairs.Add(strings.Repeat("k", 100), "value")

	var out bytes.Buffer
	pairs.Render(&out)

	line := strings.TrimRight(out.String(), "\n")
	if len(line) > 40 {
		t.Errorf("line is %d characters, want the key truncated: %q", len(line), line)
	}
	if !strings.Contains(line, "…") {
		t.Errorf("line = %q, want the truncation marked", line)
	}
	if !strings.HasSuffix(line, "value") {
		t.Errorf("line = %q, want the value kept", line)
	}
}

func TestPairsWithoutAValueRenderAsPlainLines(t *testing.T) {
	pairs := NewPairs(0)
	pairs.Add("just a line", "")

	var out bytes.Buffer
	pairs.Render(&out)

	if got := strings.TrimRight(out.String(), "\n"); got != "    just a line" {
		t.Errorf("line = %q, want no trailing padding", got)
	}
}

func TestTablesNeverLeaveTrailingWhitespace(t *testing.T) {
	// Trailing spaces survive a copy-paste and show up in a diff as changes
	// that are not there.
	table := NewTable("ID", "STATE", "NOTE")
	table.Add("1", "ACTIVE", "")
	table.Add("22", "REVOKED", "a note")

	var out bytes.Buffer
	table.Render(&out)

	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line %q has trailing whitespace", line)
		}
	}
}

func TestTableColumnsHoldTheirWidthLimit(t *testing.T) {
	table := NewTable("COMMAND", "REASON").WithWidths(10, 0)
	table.Add(strings.Repeat("x", 100), "a reason that is not capped at all")

	var out bytes.Buffer
	table.Render(&out)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	command := strings.Fields(lines[1])[0]
	if len([]rune(command)) != 10 {
		t.Errorf("command cell = %q (%d runes), want 10", command, len([]rune(command)))
	}
	if !strings.HasSuffix(lines[1], "a reason that is not capped at all") {
		t.Errorf("the uncapped column was truncated: %q", lines[1])
	}
}

func TestTruncateCountsRunesNotBytes(t *testing.T) {
	// A path with non-ASCII characters must not be cut mid-character or
	// truncated more aggressively than an ASCII one.
	tests := map[string]struct {
		text  string
		limit int
		want  string
	}{
		"nothing to do":  {"short", 10, "short"},
		"no limit":       {"anything at all", 0, "anything at all"},
		"exactly at":     {"12345", 5, "12345"},
		"one over":       {"123456", 5, "1234…"},
		"multibyte":      {"пример/файл.txt", 8, "пример/…"},
		"a tiny limit":   {"abcdef", 1, "…"},
		"an empty value": {"", 5, ""},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := Truncate(tc.text, tc.limit); got != tc.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tc.text, tc.limit, got, tc.want)
			}
		})
	}
}

func TestTimeFormattingIsTheSameEverywhere(t *testing.T) {
	at := time.Date(2026, 8, 16, 14, 5, 0, 0, time.UTC)
	formatted := FormatTime(&at)

	if len(formatted) != len(TimeFormat) {
		t.Errorf("FormatTime = %q, want the shape of %q", formatted, TimeFormat)
	}
	if FormatTime(nil) != "-" {
		t.Errorf("an unset time = %q, want -", FormatTime(nil))
	}
	var zero time.Time
	if FormatTime(&zero) != "-" {
		t.Errorf("a zero time = %q, want -", FormatTime(&zero))
	}
}

func TestEmptyValuesReadAsDashes(t *testing.T) {
	// A blank cell looks like a rendering bug; a dash reads as "nothing here".
	for _, text := range []string{"", " ", "\t", "\n"} {
		if got := Dash(text); got != "-" {
			t.Errorf("Dash(%q) = %q, want -", text, got)
		}
	}
	if got := Dash("value"); got != "value" {
		t.Errorf("Dash kept %q", got)
	}
}

// fieldValue reads back the value of one labeled line of a detail view.
//
// Tests assert on the value rather than on `"rule:      R2"`, so that changing
// a label's width is a layout change rather than a test failure — which is what
// it should be.
func fieldValue(out, label string) (string, bool) {
	prefix := "  " + label + ":"
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", false
}

// assertField checks that a detail view carries a labeled value.
func assertField(t *testing.T, out, label, want string) {
	t.Helper()
	got, found := fieldValue(out, label)
	if !found {
		t.Errorf("output has no %q field:\n%s", label, out)
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", label, got, want)
	}
}
