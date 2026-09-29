package main

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func deleteNote(s *store, name, ifMatch string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("DELETE", "/v1/file?path="+url.QueryEscape(name), nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	if ifMatch != "" {
		r.Header.Set("If-Match", ifMatch)
	}
	w := httptest.NewRecorder()
	handler(s, testToken).ServeHTTP(w, r)
	return w
}

func TestDeleteMovesNotesToTrash(t *testing.T) {
	s, dir, _ := fixture(t)
	s.now = func() time.Time { return time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC) }
	if err := os.MkdirAll(filepath.Join(dir, "日记"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("日记/今天.md", "第一版")
	w := deleteNote(s, "日记/今天.md", "")
	var result struct {
		TrashedTo string `json:"trashedTo"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.TrashedTo != ".trash/日记/今天.md" {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "日记", "今天.md")); !os.IsNotExist(err) {
		t.Fatal("note still in place", err)
	}
	// Deleting the same path again keeps the earlier copy.
	write("日记/今天.md", "第二版")
	if w := deleteNote(s, "日记/今天.md", version([]byte("第二版"))); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for name, want := range map[string]string{"今天.md": "第一版", "今天-20260929-110000.md": "第二版"} {
		if data, err := os.ReadFile(filepath.Join(dir, ".trash", "日记", name)); err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", name, data, err)
		}
	}
	if files, _, err := s.list(t.Context()); err != nil || len(files) != 0 {
		t.Fatal("trash is listed", files, err)
	}
}

func TestDeleteChecksRevisionAndPath(t *testing.T) {
	s, dir, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := deleteNote(s, "a.md", version([]byte("old"))); w.Code != 412 {
		t.Fatal("changed note deleted", w.Code)
	}
	for name, code := range map[string]int{"missing.md": 404, "../a.md": 400, "a.txt": 400} {
		if w := deleteNote(s, name, ""); w.Code != code {
			t.Errorf("%s: %d, want %d", name, w.Code, code)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.md")); err != nil {
		t.Fatal(err)
	}
}
