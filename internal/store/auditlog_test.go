package store

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRecordAndListAuditLog(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		Actor: "admin:203.0.113.1", UserAgent: "test-agent",
		Action: "communities.reset", ObjectType: "mode", ObjectID: "1",
		Before: `{"a":1}`, After: `{"a":2}`,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}

	entries, total, err := s.ListAuditLog(ctx, AuditLogFilter{}, 50, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("total=%d len=%d, want 1 and 1", total, len(entries))
	}
	e := entries[0]
	if e.Actor != "admin:203.0.113.1" || e.UserAgent != "test-agent" ||
		e.Action != "communities.reset" || e.ObjectType != "mode" || e.ObjectID != "1" ||
		e.Before != `{"a":1}` || e.After != `{"a":2}` {
		t.Fatalf("entry = %+v, fields don't round-trip", e)
	}
	if e.RecordedAt.IsZero() {
		t.Fatal("RecordedAt is zero, want the insert time")
	}
}

func TestListAuditLogFilters(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	must := func(e AuditLogEntry) {
		if err := s.RecordAuditLog(ctx, e); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	must(AuditLogEntry{Actor: "admin:1.1.1.1", Action: "communities.reset", ObjectType: "mode", ObjectID: "1"})
	must(AuditLogEntry{Actor: "user:42", Action: "user.mode_changed", ObjectType: "user", ObjectID: "42"})
	must(AuditLogEntry{Actor: "admin:1.1.1.1", Action: "feed.enabled_changed", ObjectType: "feed", ObjectID: "7"})

	tests := []struct {
		name   string
		filter AuditLogFilter
		want   int
	}{
		{"by actor", AuditLogFilter{Actor: "admin:1.1.1.1"}, 2},
		{"by action", AuditLogFilter{Action: "user.mode_changed"}, 1},
		{"by object_type", AuditLogFilter{ObjectType: "feed"}, 1},
		{"by object_id", AuditLogFilter{ObjectType: "mode", ObjectID: "1"}, 1},
		{"no match", AuditLogFilter{Actor: "user:999"}, 0},
		{"no filter", AuditLogFilter{}, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, total, err := s.ListAuditLog(ctx, tc.filter, 50, 0)
			if err != nil {
				t.Fatal(err)
			}
			if total != tc.want {
				t.Fatalf("total = %d, want %d", total, tc.want)
			}
		})
	}
}

func TestListAuditLogTimeRange(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: now.Add(-48 * time.Hour), Actor: "admin:1.1.1.1", Action: "a", ObjectType: "t", ObjectID: "1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: now, Actor: "admin:1.1.1.1", Action: "a", ObjectType: "t", ObjectID: "1",
	}); err != nil {
		t.Fatal(err)
	}

	_, total, err := s.ListAuditLog(ctx, AuditLogFilter{Since: now.Add(-24 * time.Hour)}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("Since filter: total = %d, want 1 (only the recent entry)", total)
	}

	_, total, err = s.ListAuditLog(ctx, AuditLogFilter{Until: now.Add(-24 * time.Hour)}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("Until filter: total = %d, want 1 (only the old entry)", total)
	}
}

func TestListAuditLogPagination(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if err := s.RecordAuditLog(ctx, AuditLogEntry{
			Actor: "admin:1.1.1.1", Action: "a", ObjectType: "t", ObjectID: "1",
		}); err != nil {
			t.Fatal(err)
		}
	}

	entries, total, err := s.ListAuditLog(ctx, AuditLogFilter{}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5 (independent of limit)", total)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (limited)", len(entries))
	}

	entries2, _, err := s.ListAuditLog(ctx, AuditLogFilter{}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries2) != 1 {
		t.Fatalf("len(entries2) = %d, want 1 (offset 4 of 5 leaves 1)", len(entries2))
	}
}

func TestListAuditLogOrderNewestFirst(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.RecordAuditLog(ctx, AuditLogEntry{RecordedAt: now.Add(-time.Hour), Actor: "a", Action: "first", ObjectType: "t", ObjectID: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuditLog(ctx, AuditLogEntry{RecordedAt: now, Actor: "a", Action: "second", ObjectType: "t", ObjectID: "1"}); err != nil {
		t.Fatal(err)
	}

	entries, _, err := s.ListAuditLog(ctx, AuditLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Action != "second" || entries[1].Action != "first" {
		t.Fatalf("entries = %+v, want [second, first] (newest first)", entries)
	}
}

func TestPurgeAuditLog(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: now.Add(-40 * 24 * time.Hour), Actor: "a", Action: "old", ObjectType: "t", ObjectID: "1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: now.Add(-1 * time.Hour), Actor: "a", Action: "recent", ObjectType: "t", ObjectID: "1",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeAuditLog(ctx, 30); err != nil {
		t.Fatalf("purge: %v", err)
	}

	entries, total, err := s.ListAuditLog(ctx, AuditLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(entries) != 1 || entries[0].Action != "recent" {
		t.Fatalf("after purge: entries = %+v, want only \"recent\" to survive", entries)
	}
}

// TestPurgeAuditLogExtremeRetentionDoesNotDeleteEverything guards against
// time.Duration(days)*24*time.Hour overflowing int64 for an absurdly large
// days value (e.g. a fat-fingered retention-days setting) — an overflow
// can flip the sign, turning a "purge older than N days" cutoff into one
// in the future, which would delete every row on the next purge.
func TestPurgeAuditLogExtremeRetentionDoesNotDeleteEverything(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		RecordedAt: time.Now().UTC(), Actor: "a", Action: "recent", ObjectType: "t", ObjectID: "1",
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeAuditLog(ctx, math.MaxInt); err != nil {
		t.Fatalf("purge: %v", err)
	}

	_, total, err := s.ListAuditLog(ctx, AuditLogFilter{}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1 (an absurd retention-days value must not delete everything via integer overflow)", total)
	}
}

// TestTruncateUserAgent covers the boundary cases directly: short values
// pass through untouched, an oversized value is cut to exactly the limit,
// and a multi-byte UTF-8 rune straddling the cut point is excluded
// entirely rather than split (which would store invalid UTF-8).
func TestTruncateUserAgent(t *testing.T) {
	short := "Mozilla/5.0 (test)"
	if got := truncateUserAgent(short); got != short {
		t.Fatalf("short value changed: got %q, want unchanged %q", got, short)
	}

	long := strings.Repeat("A", maxAuditUserAgent+100)
	got := truncateUserAgent(long)
	if len(got) != maxAuditUserAgent {
		t.Fatalf("len(got) = %d, want exactly %d", len(got), maxAuditUserAgent)
	}

	// A 3-byte rune ("€", U+20AC) placed so it straddles the cut boundary.
	runeStraddling := strings.Repeat("A", maxAuditUserAgent-1) + "€" + strings.Repeat("B", 100)
	got = truncateUserAgent(runeStraddling)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated value is not valid UTF-8: %q", got)
	}
	if len(got) >= maxAuditUserAgent {
		t.Fatalf("len(got) = %d, want < %d (the straddling rune must be excluded, not split)", len(got), maxAuditUserAgent)
	}
}

// TestRecordAuditLogTruncatesOversizedUserAgent checks the actual insert
// path: a caller-supplied UserAgent far beyond any real browser's, as an
// authenticated caller could send on every request via the User-Agent
// header (no length limit of its own, up to the server's own header-size
// cap), must not be stored verbatim — the next request's row would just
// keep growing the table.
func TestRecordAuditLogTruncatesOversizedUserAgent(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	oversized := strings.Repeat("A", maxAuditUserAgent*4)
	if err := s.RecordAuditLog(ctx, AuditLogEntry{
		Actor: "user:1", UserAgent: oversized, Action: "test.oversized_ua", ObjectType: "t", ObjectID: "1",
	}); err != nil {
		t.Fatal(err)
	}

	entries, total, err := s.ListAuditLog(ctx, AuditLogFilter{Action: "test.oversized_ua"}, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
	if len(entries[0].UserAgent) > maxAuditUserAgent {
		t.Fatalf("stored UserAgent len = %d, want <= %d", len(entries[0].UserAgent), maxAuditUserAgent)
	}
}
