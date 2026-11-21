package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func callCleanup(h http.Handler, route, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, route, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCleanupAuthAndQueryValidation(t *testing.T) {
	s, _, h := fixture(t)
	s.publicImages = true

	// Cleanup endpoint must require Bearer token even when publicImages is true.
	for _, tok := range []string{"", "wrong-token"} {
		if w := callCleanup(h, "/v1/images/cleanup", tok); w.Code != 401 {
			t.Fatalf("token %q returned %d, want 401", tok, w.Code)
		}
	}

	for _, route := range []string{
		"/v1/images/cleanup?dryRun=maybe",
		"/v1/images/cleanup?minAge=invalid",
		"/v1/images/cleanup?minAge=-1h",
		"/v1/images/cleanup?minAge=-1d",
	} {
		if w := callCleanup(h, route, testToken); w.Code != 400 {
			t.Fatalf("route %q returned %d (%s), want 400", route, w.Code, w.Body.String())
		}
	}

	for _, route := range []string{
		"/v1/images/cleanup?minAge=0",
		"/v1/images/cleanup?minAge=7d&dryRun=1",
		"/v1/images/cleanup?minAge=168h&dryRun=yes",
	} {
		if w := callCleanup(h, route, testToken); w.Code != 200 {
			t.Fatalf("route %q returned %d (%s), want 200", route, w.Code, w.Body.String())
		}
	}

	if d, err := parseCleanupInterval(""); err != nil || d != 24*time.Hour {
		t.Fatalf("empty interval = (%v, %v), want 24h", d, err)
	}
	for _, off := range []string{"0", "off", "FALSE", "disabled"} {
		if d, err := parseCleanupInterval(off); err != nil || d != 0 {
			t.Fatalf("interval %q = (%v, %v), want 0", off, d, err)
		}
	}
	if d, err := parseCleanupInterval("2d"); err != nil || d != 48*time.Hour {
		t.Fatalf("interval 2d = (%v, %v)", d, err)
	}
	for _, bad := range []string{"-1h", "-1d", "abc"} {
		if _, err := parseCleanupInterval(bad); err == nil {
			t.Fatalf("expected error for interval %q", bad)
		}
	}
	for _, bad := range []string{"0", "0h", "-1h", "bad"} {
		if _, err := parseCleanupMinAgeEnv(bad); err == nil {
			t.Fatalf("expected error for minAge %q", bad)
		}
	}
	if d, err := parseCleanupMinAgeEnv("14d"); err != nil || d != 14*24*time.Hour {
		t.Fatalf("minAge 14d = (%v, %v)", d, err)
	}
}

func TestCleanupDryRunAndApply(t *testing.T) {
	s, dir, h := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if err := os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	writeImg := func(name string, age time.Duration) string {
		p := filepath.Join(dir, "media", name)
		data := testImage(t, "png")
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		mtime := now.Add(-age)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}

	usedPath := writeImg("used.png", 10*24*time.Hour)
	oldOrphanPath := writeImg("old-orphan.png", 8*24*time.Hour)
	freshOrphanPath := writeImg("fresh-orphan.png", 2*24*time.Hour)

	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("![](media/used.png)\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Default dryRun is true: old orphan is reported but stays in place.
	w := callCleanup(h, "/v1/images/cleanup", testToken)
	if w.Code != 200 {
		t.Fatalf("dryRun cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var dry cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &dry); err != nil {
		t.Fatal(err)
	}
	if !dry.DryRun || dry.MinAge != "168h0m0s" || dry.ScannedImages != 3 || dry.ScannedNotes != 1 || len(dry.Images) != 1 {
		t.Fatalf("unexpected dryRun result: %+v", dry)
	}
	if dry.Images[0].Path != "media/old-orphan.png" || dry.Images[0].TrashedTo != "" || dry.TotalBytes != dry.Images[0].Size {
		t.Fatalf("unexpected dryRun image entry: %+v", dry.Images[0])
	}
	if _, err := os.Stat(oldOrphanPath); err != nil {
		t.Fatal("dryRun moved file", err)
	}

	// Apply move with dryRun=false.
	w = callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("apply cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var applied cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.DryRun || len(applied.Images) != 1 || applied.Images[0].TrashedTo != ".trash/media/old-orphan.png" {
		t.Fatalf("unexpected apply result: %+v", applied)
	}
	if _, err := os.Stat(oldOrphanPath); !os.IsNotExist(err) {
		t.Fatal("old orphan still exists at original location")
	}
	if _, err := os.Stat(filepath.Join(dir, ".trash/media/old-orphan.png")); err != nil {
		t.Fatal("old orphan missing from .trash", err)
	}
	if _, err := os.Stat(usedPath); err != nil {
		t.Fatal("used image removed", err)
	}
	if _, err := os.Stat(freshOrphanPath); err != nil {
		t.Fatal("fresh orphan within grace period removed", err)
	}
}

func TestCleanupReferenceSyntaxAndURLEncoding(t *testing.T) {
	s, dir, h := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if err := os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}

	names := []string{
		"plain.png",
		"rel.png",
		"with space.png",
		"ref.png",
		"html.png",
		"wiki.png",
		"wikisize.png",
		"query.png",
		"架构-图.png",
		"架构 图.png",
		"a+b.png",
		"dead.png",
	}
	old := now.Add(-14 * 24 * time.Hour)
	for _, name := range names {
		p := filepath.Join(dir, "media", name)
		if err := os.WriteFile(p, testImage(t, "png"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	noteBody := strings.Join([]string{
		`![](media/plain.png)`,
		`![desc](<media/with space.png> "title")`,
		`[r]: media/ref.png`,
		`<img src="./media/html.png" alt="x">`,
		`![[wiki.png]]`,
		`![[media/wikisize.png|300]]`,
		`![](media/query.png?v=1#top)`,
		`![](media/%e6%9e%b6%e6%9e%84-%e5%9b%be.png)`,
		`![](http://nas:8789/v1/image?path=media%2F%E6%9E%B6%E6%9E%84+%E5%9B%BE.png)`,
		`![](media/a%2Bb.png)`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "main.md"), []byte(noteBody), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "child.markdown"), []byte(`![](../media/rel.png)`), 0600); err != nil {
		t.Fatal(err)
	}

	w := callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var res cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].Path != "media/dead.png" {
		t.Fatalf("expected only media/dead.png to be cleaned, got %+v", res.Images)
	}
}

func TestCleanupLargeNoteStreamingAcrossChunkBoundary(t *testing.T) {
	s, dir, h := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if err := os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-14 * 24 * time.Hour)
	for _, name := range []string{"boundary-图.png", "tail.png", "orphan.png"} {
		p := filepath.Join(dir, "media", name)
		if err := os.WriteFile(p, testImage(t, "png"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	// Build a >2.5 MiB note (bigger than maxFileSize) with a URL-encoded link
	// straddling the 64 KiB read chunk boundary and another link at the very end.
	var big bytes.Buffer
	big.WriteString(strings.Repeat("a", cleanupReadChunkSize-8))
	big.WriteString("![](media/boundary-%E5%9B%BE.png)\n")
	big.WriteString(strings.Repeat("b", (2<<20)+512*1024))
	big.WriteString("\n![](media/tail.png)\n")
	if err := os.WriteFile(filepath.Join(dir, "huge.md"), big.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	w := callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var res cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].Path != "media/orphan.png" {
		t.Fatalf("expected only media/orphan.png, got %+v", res.Images)
	}
}

func TestCleanupPreservesTrashNoteReferences(t *testing.T) {
	s, dir, h := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	for _, sub := range []string{"media", ".trash", ".git"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-14 * 24 * time.Hour)
	for _, name := range []string{"in-trash.png", "in-git.png"} {
		p := filepath.Join(dir, "media", name)
		if err := os.WriteFile(p, testImage(t, "png"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, ".trash", "deleted.md"), []byte("![](media/in-trash.png)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "COMMIT_EDITMSG.md"), []byte("![](media/in-git.png)\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// .trash note protects in-trash.png; .git note does not protect in-git.png.
	w := callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var res cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].Path != "media/in-git.png" {
		t.Fatalf("expected only media/in-git.png to be cleaned, got %+v", res.Images)
	}
	if _, err := os.Stat(filepath.Join(dir, "media", "in-trash.png")); err != nil {
		t.Fatal("image referenced by trashed note was removed", err)
	}

	// Once the trashed note is permanently removed, the image becomes eligible.
	if err := os.Remove(filepath.Join(dir, ".trash", "deleted.md")); err != nil {
		t.Fatal(err)
	}
	w = callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("second cleanup failed: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 1 || res.Images[0].Path != "media/in-trash.png" {
		t.Fatalf("expected media/in-trash.png after removing trashed note, got %+v", res.Images)
	}
}

func TestCleanupScopedDirSafetyAndCollisions(t *testing.T) {
	s, dir, h := fixture(t)
	s.attachments = "assets/img"
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	// Missing attachments directory returns empty 200.
	w := callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("missing attachments dir returned %d (%s)", w.Code, w.Body.String())
	}

	imgDir := filepath.Join(dir, "assets", "img")
	if err := os.MkdirAll(filepath.Join(imgDir, "manual-subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".trash", "assets", "img"), 0700); err != nil {
		t.Fatal(err)
	}

	old := now.Add(-14 * 24 * time.Hour)
	touchOld := func(p string, data []byte) {
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}

	// Non-image, hidden temp file, and user subdir file must be left untouched.
	touchOld(filepath.Join(imgDir, "notes.pdf"), []byte("pdf"))
	touchOld(filepath.Join(imgDir, ".floral-image-tmp.png"), testImage(t, "png"))
	touchOld(filepath.Join(imgDir, "manual-subdir", "keep.png"), testImage(t, "png"))
	// Collision in .trash/assets/img/
	touchOld(filepath.Join(imgDir, "dup.png"), testImage(t, "png"))
	if err := os.WriteFile(filepath.Join(dir, ".trash", "assets", "img", "dup.png"), []byte("v1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".trash", "assets", "img", "dup-20261121-120000.png"), []byte("v2"), 0600); err != nil {
		t.Fatal(err)
	}

	w = callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var res cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.ScannedImages != 1 || len(res.Images) != 1 || res.Images[0].TrashedTo != ".trash/assets/img/dup-20261121-120000-2.png" {
		t.Fatalf("unexpected collision result: %+v", res)
	}
	for _, keep := range []string{
		filepath.Join(imgDir, "notes.pdf"),
		filepath.Join(imgDir, ".floral-image-tmp.png"),
		filepath.Join(imgDir, "manual-subdir", "keep.png"),
		filepath.Join(dir, ".trash", "assets", "img", "dup.png"),
		filepath.Join(dir, ".trash", "assets", "img", "dup-20261121-120000.png"),
	} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected %s to remain untouched: %v", keep, err)
		}
	}

	// Symlinked attachments directory must be rejected.
	outside := t.TempDir()
	s2, dir2, h2 := fixture(t)
	s2.now = func() time.Time { return now }
	if err := os.Symlink(outside, filepath.Join(dir2, "media")); err != nil {
		t.Fatal(err)
	}
	if w := callCleanup(h2, "/v1/images/cleanup?dryRun=false", testToken); w.Code != 400 {
		t.Fatalf("symlinked media dir returned %d, want 400", w.Code)
	}
}

func TestCleanupDedupReuseRefreshesGracePeriod(t *testing.T) {
	s, dir, h := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	data := testImage(t, "png")
	first := callImage(h, http.MethodPost, "Draft.md", data)
	if first.Code != 200 {
		t.Fatalf("first upload failed: %d %s", first.Code, first.Body.String())
	}
	var uploaded uploadedImage
	if err := json.Unmarshal(first.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}

	// Simulate an unreferenced 30-day-old image on disk.
	diskPath := filepath.Join(dir, uploaded.Path)
	old := now.Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(diskPath, old, old); err != nil {
		t.Fatal(err)
	}

	// Re-pasting identical bytes reuses the file and refreshes its grace period.
	second := callImage(h, http.MethodPost, "NewDraft.md", data)
	if second.Code != 200 {
		t.Fatalf("second upload failed: %d %s", second.Code, second.Body.String())
	}
	var reused uploadedImage
	if err := json.Unmarshal(second.Body.Bytes(), &reused); err != nil {
		t.Fatal(err)
	}
	if reused.Path != uploaded.Path {
		t.Fatalf("expected deduplicated reuse of %s, got %s", uploaded.Path, reused.Path)
	}

	// Even before NewDraft.md is saved, cleanup must NOT delete the reused image.
	w := callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken)
	if w.Code != 200 {
		t.Fatalf("cleanup failed: %d %s", w.Code, w.Body.String())
	}
	var res cleanupResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Images) != 0 {
		t.Fatalf("reused image within refreshed grace period was cleaned: %+v", res.Images)
	}
	info, err := os.Stat(diskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(now) {
		t.Fatalf("expected disk mtime refreshed to %v, got %v", now, info.ModTime())
	}
}

func TestCleanupFailClosedOnReadError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses chmod 0000 read checks")
	}
	s, dir, h := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if err := os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "media", "orphan.png")
	if err := os.WriteFile(orphan, testImage(t, "png"), 0600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-14 * 24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	unreadable := filepath.Join(dir, "locked.md")
	if err := os.WriteFile(unreadable, []byte("![](media/orphan.png)"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unreadable, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0600) })

	if w := callCleanup(h, "/v1/images/cleanup?dryRun=false", testToken); w.Code != 403 {
		t.Fatalf("expected 403 on unreadable note, got %d (%s)", w.Code, w.Body.String())
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("orphan image was removed despite scan read error", err)
	}
}

func TestCleanupWriteSeqIncrementsOnWriteAndTrash(t *testing.T) {
	s, _, h := fixture(t)
	before := s.writeSeq.Load()
	w := call(h, http.MethodPut, "a.md", "hello", "", "*")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	afterWrite := s.writeSeq.Load()
	if afterWrite <= before {
		t.Fatalf("writeSeq did not advance on write: %d -> %d", before, afterWrite)
	}
	w = deleteNote(s, "a.md", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if s.writeSeq.Load() <= afterWrite {
		t.Fatalf("writeSeq did not advance on trash: %d -> %d", afterWrite, s.writeSeq.Load())
	}
}

func TestCleanupBackgroundLoop(t *testing.T) {
	s, dir, _ := fixture(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	if err := os.MkdirAll(filepath.Join(dir, "media"), 0700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "media", "bg-orphan.png")
	if err := os.WriteFile(orphan, testImage(t, "png"), 0600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := startImageCleanupLoop(ctx, s, 15*time.Millisecond, 7*24*time.Hour)

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".trash", "media", "bg-orphan.png")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background cleanup loop did not move orphan image in time")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background cleanup loop did not stop after context cancellation")
	}

	disabledDone := startImageCleanupLoop(context.Background(), s, 0, 7*24*time.Hour)
	select {
	case <-disabledDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("disabled cleanup loop did not close immediately")
	}
}
