package updater

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDecisionsAreAppendedInOrder(t *testing.T) {
	store := newStore(t)

	for _, event := range []string{EventCheckOK, EventPromptShown, EventChoiceSkip} {
		if err := store.Append(UpdateDecision{Event: event, TargetVersion: "0.2.0"}); err != nil {
			t.Fatalf("Append %s: %v", event, err)
		}
	}

	entries := store.Tail(10)
	if len(entries) != 3 {
		t.Fatalf("tail = %d entries, want 3", len(entries))
	}
	if entries[0].Event != EventCheckOK || entries[2].Event != EventChoiceSkip {
		t.Errorf("entries out of order: %+v", entries)
	}
	if entries[0].At.IsZero() {
		t.Error("an entry without a time must be stamped when it is written")
	}
	if entries[1].TargetVersion != "0.2.0" {
		t.Errorf("target version = %q", entries[1].TargetVersion)
	}
}

func TestTailReturnsTheMostRecentEntries(t *testing.T) {
	store := newStore(t)
	for i := 0; i < 25; i++ {
		if err := store.Append(UpdateDecision{Event: EventCheckOK, Detail: fmt.Sprintf("check %d", i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	entries := store.Tail(20)
	if len(entries) != 20 {
		t.Fatalf("tail = %d entries, want 20", len(entries))
	}
	if entries[0].Detail != "check 5" || entries[19].Detail != "check 24" {
		t.Errorf("tail returned %q…%q, want check 5…check 24", entries[0].Detail, entries[19].Detail)
	}
}

func TestAMissingLogIsEmptyRatherThanAnError(t *testing.T) {
	if entries := newStore(t).Tail(10); len(entries) != 0 {
		t.Errorf("tail of a missing log = %v, want empty", entries)
	}
}

func TestTheLogIsTrimmedToItsRetentionLimit(t *testing.T) {
	// The log is written on every check on every machine; unbounded growth is a
	// slow leak in a home directory nobody looks at.
	store := newStore(t)
	for i := 0; i < historyLimit+30; i++ {
		if err := store.Append(UpdateDecision{Event: EventCheckOK, Detail: fmt.Sprintf("%d", i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	data, err := os.ReadFile(store.HistoryPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := strings.Count(strings.TrimSpace(string(data)), "\n") + 1
	if lines != historyLimit {
		t.Errorf("log has %d lines, want %d", lines, historyLimit)
	}

	entries := store.Tail(1)
	if len(entries) != 1 || entries[0].Detail != fmt.Sprintf("%d", historyLimit+29) {
		t.Errorf("trimming kept the wrong end: %+v", entries)
	}
}

func TestACorruptLineIsSkippedRatherThanFatal(t *testing.T) {
	store := newStore(t)
	if err := store.Append(UpdateDecision{Event: EventCheckOK, Detail: "first"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	appendRaw(t, store.HistoryPath(), "{ truncated")
	if err := store.Append(UpdateDecision{Event: EventCheckOK, Detail: "third"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries := store.Tail(10)
	if len(entries) != 2 || entries[0].Detail != "first" || entries[1].Detail != "third" {
		t.Errorf("entries = %+v, want the two readable ones", entries)
	}
}

func TestAnExplicitTimeIsKeptInUTC(t *testing.T) {
	store := newStore(t)
	when := time.Date(2026, 8, 16, 12, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	if err := store.Append(UpdateDecision{At: when, Event: EventUpdateOK}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	entries := store.Tail(1)
	if len(entries) != 1 {
		t.Fatalf("tail = %d entries", len(entries))
	}
	if !entries[0].At.Equal(when) {
		t.Errorf("at = %s, want the same instant as %s", entries[0].At, when)
	}
	if entries[0].At.Location() != time.UTC {
		t.Errorf("at is in %s, want UTC so entries from different machines sort", entries[0].At.Location())
	}
}

func appendRaw(t *testing.T, path, line string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(line + "\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
}
