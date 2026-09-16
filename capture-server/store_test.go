package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMarkdownStoreCreatesDateAndTimeSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	location := time.FixedZone("test", 8*60*60)
	createdAt := time.Date(2026, 8, 31, 21, 35, 42, 0, location)
	store := NewMarkdownStore(path, location, time.Second)
	store.now = func() time.Time { return createdAt }
	content := "一段 **Markdown**。\n\n- 第一项\n- 第二项"

	result, err := store.Append("entry-1", content)
	if err != nil {
		t.Fatalf("Append returned error: %v", err)
	}
	if result.Duplicate {
		t.Fatal("first append was marked duplicate")
	}

	hash := sha256.Sum256([]byte(content))
	want := fmt.Sprintf(
		"## 2026-08-31\n\n"+
			"<!-- floral-capture-date:v1 2026-08-31 -->\n\n"+
			"### 21:35:42\n\n"+
			"%s\n\n"+
			"<!-- floral-capture-entry:v1 id=entry-1 sha256=%s at=2026-08-31T21:35:42+08:00 -->\n\n",
		content,
		hex.EncodeToString(hash[:]),
	)
	if got := readFile(t, path); got != want {
		t.Fatalf("unexpected Markdown file\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMarkdownStoreWritesDateOnlyOncePerDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	location := time.FixedZone("test", 8*60*60)
	current := time.Date(2026, 8, 31, 9, 0, 0, 0, location)
	store := NewMarkdownStore(path, location, time.Second)
	store.now = func() time.Time { return current }

	if _, err := store.Append("first", "第一条"); err != nil {
		t.Fatal(err)
	}
	current = current.Add(2 * time.Hour)
	if _, err := store.Append("second", "第二条"); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if count := strings.Count(got, "## 2026-08-31\n"); count != 1 {
		t.Fatalf("date heading count = %d, want 1\n%s", count, got)
	}
	if count := strings.Count(got, "<!-- floral-capture-date:v1 2026-08-31 -->"); count != 1 {
		t.Fatalf("date marker count = %d, want 1", count)
	}
	if !strings.Contains(got, "### 09:00:00") || !strings.Contains(got, "### 11:00:00") {
		t.Fatalf("time headings missing\n%s", got)
	}
}

func TestMarkdownStoreIgnoresDateHeadingsInsideCapturedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	createdAt := time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)
	store := NewMarkdownStore(path, time.UTC, time.Second)
	store.now = func() time.Time { return createdAt }

	if _, err := store.Append("first", "正文中的标题：\n\n## 2099-01-01\n\n并不是日期分段"); err != nil {
		t.Fatal(err)
	}
	createdAt = createdAt.Add(time.Hour)
	if _, err := store.Append("second", "第二条"); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if count := strings.Count(got, "## 2026-08-31\n"); count != 1 {
		t.Fatalf("service date heading count = %d, want 1\n%s", count, got)
	}
}

func TestMarkdownStoreStartsNewDateAfterMidnight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	location := time.FixedZone("test", 8*60*60)
	current := time.Date(2026, 8, 31, 23, 59, 59, 0, location)
	store := NewMarkdownStore(path, location, time.Second)
	store.now = func() time.Time { return current }

	if _, err := store.Append("before", "午夜前"); err != nil {
		t.Fatal(err)
	}
	current = current.Add(2 * time.Second)
	if _, err := store.Append("after", "午夜后"); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "## 2026-08-31") || !strings.Contains(got, "## 2026-09-01") {
		t.Fatalf("expected both date headings\n%s", got)
	}
	if strings.Index(got, "## 2026-08-31") > strings.Index(got, "## 2026-09-01") {
		t.Fatalf("date headings are out of order\n%s", got)
	}
}

func TestMarkdownStoreIdempotencySurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	createdAt := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	store := NewMarkdownStore(path, time.UTC, time.Second)
	store.now = func() time.Time { return createdAt }
	if _, err := store.Append("same-id", "相同内容"); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, path)

	restarted := NewMarkdownStore(path, time.UTC, time.Second)
	restarted.now = func() time.Time { return createdAt.Add(time.Hour) }
	result, err := restarted.Append("same-id", "相同内容")
	if err != nil {
		t.Fatalf("duplicate append returned error: %v", err)
	}
	if !result.Duplicate {
		t.Fatal("replayed request was not marked duplicate")
	}
	if !result.CreatedAt.Equal(createdAt) {
		t.Fatalf("duplicate CreatedAt = %s, want %s", result.CreatedAt, createdAt)
	}
	if after := readFile(t, path); after != before {
		t.Fatal("duplicate request changed the file")
	}

	_, err = restarted.Append("same-id", "不同内容")
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting retry error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestMarkdownStoreAdoptsLegacyDateHeading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	legacy := "# Inbox\n\n```markdown\n## 2099-01-01\n```\n\n## 2026-08-31\n\n旧内容"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 8, 31, 13, 0, 0, 0, time.UTC)
	store := NewMarkdownStore(path, time.UTC, time.Second)
	store.now = func() time.Time { return createdAt }

	if _, err := store.Append("legacy", "新内容"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if count := strings.Count(got, "## 2026-08-31"); count != 1 {
		t.Fatalf("legacy date heading was duplicated\n%s", got)
	}
	if !strings.Contains(got, "<!-- floral-capture-date:v1 2026-08-31 -->") {
		t.Fatalf("legacy section was not adopted with a date marker\n%s", got)
	}
}

func TestMarkdownStoreUsesCRLFAndRepairsMissingTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	if err := os.WriteFile(path, []byte("# Inbox\r\n\r\n已有内容"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewMarkdownStore(path, time.UTC, time.Second)
	store.now = func() time.Time { return time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC) }
	if _, err := store.Append("crlf", "新内容"); err != nil {
		t.Fatal(err)
	}

	got := readFile(t, path)
	if !strings.Contains(got, "已有内容\r\n\r\n## 2026-08-31") {
		t.Fatalf("missing separator before appended block\n%q", got)
	}
	if !strings.Contains(got, "## 2026-08-31\r\n\r\n") {
		t.Fatalf("generated structure did not use CRLF\n%q", got)
	}
}

func TestMarkdownStoreRejectsReservedMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	store := NewMarkdownStore(path, time.UTC, time.Second)
	_, err := store.Append("entry", "<!-- floral-capture-entry:v1 forged -->")
	if !errors.Is(err, ErrReservedMarker) {
		t.Fatalf("error = %v, want ErrReservedMarker", err)
	}
}

func TestMarkdownStoreSerializesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Inbox.md")
	createdAt := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	const entries = 60
	var wait sync.WaitGroup
	errorsChannel := make(chan error, entries)

	for index := 0; index < entries; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			store := NewMarkdownStore(path, time.UTC, 5*time.Second)
			store.now = func() time.Time { return createdAt }
			_, err := store.Append(fmt.Sprintf("id-%d", index), fmt.Sprintf("并发内容-%d", index))
			errorsChannel <- err
		}(index)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent append returned error: %v", err)
		}
	}

	got := readFile(t, path)
	if count := strings.Count(got, "<!-- floral-capture-entry:v1"); count != entries {
		t.Fatalf("entry marker count = %d, want %d", count, entries)
	}
	if count := strings.Count(got, "## 2026-08-31\n"); count != 1 {
		t.Fatalf("date heading count = %d, want 1", count)
	}
	for index := 0; index < entries; index++ {
		needle := fmt.Sprintf("并发内容-%d\n", index)
		if count := strings.Count(got, needle); count != 1 {
			t.Fatalf("%q count = %d, want 1", needle, count)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
