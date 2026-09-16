package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testToken = "test-token-at-least-thirty-two-characters"

func fixture(t *testing.T) (*store, string, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	s := &store{root: root}
	return s, dir, handler(s, testToken)
}
func call(h http.Handler, method, path, content, match, none string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/file?path="+url.QueryEscape(path), strings.NewReader(content))
	r.Header.Set("Authorization", "Bearer "+testToken)
	if match != "" {
		r.Header.Set("If-Match", match)
	}
	if none != "" {
		r.Header.Set("If-None-Match", none)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestRoundTripAndExternalConflict(t *testing.T) {
	_, dir, h := fixture(t)
	name := "中文 ' $笔记.md"
	content := "---\r\ntitle: 保留格式\r\n---\r\n# 标题\r\n\r\n"
	w := call(h, "PUT", name, content, "", "*")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(h, "GET", name, "", "", "")
	var doc document
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Content != content || doc.Revision != w.Header().Get("ETag") {
		t.Fatal("content or version changed")
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("nvim changed this"), 0600); err != nil {
		t.Fatal(err)
	}
	w = call(h, "PUT", name, "old client draft", doc.Revision, "")
	if w.Code != 412 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(dir, name))
	if string(data) != "nvim changed this" {
		t.Fatal("overwrote external edit")
	}
}
func TestPreconditionsAndRetry(t *testing.T) {
	_, _, h := fixture(t)
	if w := call(h, "PUT", "a.md", "first", "", ""); w.Code != 428 {
		t.Fatal(w.Code)
	}
	first := call(h, "PUT", "a.md", "first", "", "*")
	rev := first.Header().Get("ETag")
	if w := call(h, "PUT", "a.md", "different", "", "*"); w.Code != 412 {
		t.Fatal(w.Code)
	}
	updated := call(h, "PUT", "a.md", "second", rev, "")
	if updated.Code != 200 {
		t.Fatal(updated.Code)
	}
	if w := call(h, "PUT", "a.md", "second", rev, ""); w.Code != 200 {
		t.Fatal("lost response retry failed", w.Code)
	}
	if w := call(h, "PUT", "a.md", "third", rev, ""); w.Code != 412 {
		t.Fatal("stale revision accepted")
	}
	if w := call(h, "PUT", "missing.md", "x", rev, ""); w.Code != 412 {
		t.Fatal(w.Code)
	}
	if w := call(h, "PUT", "folder/missing.md", "x", "", "*"); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestConfinementAndAuthentication(t *testing.T) {
	_, dir, h := fixture(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret.md", "/etc/secret.md", "x/../../secret.md", "link/secret.md", "x\\secret.md", "secret.txt"} {
		for _, method := range []string{"GET", "PUT"} {
			w := call(h, method, name, "overwrite", "", "*")
			if w.Code == 200 {
				t.Fatalf("allowed %s %s", method, name)
			}
		}
	}
	data, _ := os.ReadFile(filepath.Join(outside, "secret.md"))
	if string(data) != "secret" {
		t.Fatal("escaped root")
	}
	for _, route := range []string{"/v1/files", "/v1/file?path=a.md"} {
		r := httptest.NewRequest("GET", route, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatal(w.Code)
		}
	}
}
func TestListAndBodyLimits(t *testing.T) {
	_, dir, h := fixture(t)
	_ = os.Mkdir(filepath.Join(dir, "子目录"), 0700)
	_ = os.Mkdir(filepath.Join(dir, ".git"), 0700)
	for _, name := range []string{"b.md", "子目录/a.markdown", ".git/hidden.md", "ignore.txt"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600)
	}
	r := httptest.NewRequest("GET", "/v1/files", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var result struct {
		Files []string `json:"files"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if strings.Join(result.Files, ",") != "b.md,子目录/a.markdown" {
		t.Fatal(result.Files)
	}
	if w := call(h, "PUT", "large.md", strings.Repeat("x", maxFileSize+1), "", "*"); w.Code != 413 {
		t.Fatal(w.Code)
	}
	if w := call(h, "PUT", "bad.md", string([]byte{0xff}), "", "*"); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if w := call(h, "PUT", "empty.md", "", "", "*"); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestConcurrentWritersAndPermissions(t *testing.T) {
	s, dir, h := fixture(t)
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("initial"), 0640)
	rev := version([]byte("initial"))
	codes := make(chan int, 2)
	var wg sync.WaitGroup
	// Two store objects simulate cooperating API processes (directory flock).
	root2, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root2.Close()
	for i, api := range []http.Handler{h, handler(&store{root: root2}, testToken)} {
		wg.Add(1)
		go func(i int, api http.Handler) {
			defer wg.Done()
			codes <- call(api, "PUT", "a.md", strings.Repeat("new", i+1), rev, "").Code
		}(i, api)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for c := range codes {
		counts[c]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatal(counts)
	}
	info, _ := os.Stat(filepath.Join(dir, "a.md"))
	if info.Mode().Perm() != 0640 {
		t.Fatal("permissions changed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.write(ctx, "cancel.md", []byte("x"), "", true); err == nil {
		t.Fatal("ignored cancellation")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".floral-*"))
	if len(matches) != 0 {
		t.Fatal("temporary files leaked")
	}
}
func TestHTTPIntegration(t *testing.T) {
	_, _, h := fixture(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	r, _ := http.NewRequest("PUT", srv.URL+"/v1/file?path=Inbox.md", bytes.NewBufferString("# hello"))
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("If-None-Match", "*")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		t.Fatal(string(body))
	}
}
