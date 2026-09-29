package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func postAppend(t *testing.T, s *store, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/append", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	handler(s, testToken).ServeHTTP(w, r)
	return w
}

func TestAppendGroupsByDayAndDeduplicates(t *testing.T) {
	s, dir, _ := fixture(t)
	now := time.Date(2026, 9, 28, 14, 32, 5, 0, time.UTC)
	s.now = func() time.Time { return now }

	w := postAppend(t, s, `{"id":"a","title":"","text":"第一条\r\n"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result appendResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Path != "Chat.md" {
		t.Fatal(result, err)
	}
	now = now.Add(90 * time.Minute)
	postAppend(t, s, `{"id":"b","title":"购物  清单","text":"- 牛奶"}`)
	// A retried request after a lost response must not append twice.
	if w := postAppend(t, s, `{"id":"b","title":"购物  清单","text":"- 牛奶"}`); w.Code != 200 {
		t.Fatal(w.Code)
	}
	now = now.Add(24 * time.Hour)
	postAppend(t, s, `{"id":"c","text":"次日"}`)

	data, err := os.ReadFile(filepath.Join(dir, "Chat.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := "#### 28 September, Monday\n- [ ] `14:32` 第一条\n- [ ] `16:02` 购物 清单\n- 牛奶\n\n#### 29 September, Tuesday\n- [ ] `16:02` 次日\n"
	if string(data) != want {
		t.Fatalf("got:\n%s", data)
	}
}

func TestAppendKeepsExternalEditsAndMissingNewline(t *testing.T) {
	s, dir, _ := fixture(t)
	s.inbox = "Inbox.md"
	s.now = func() time.Time { return time.Date(2026, 9, 28, 9, 5, 0, 0, time.UTC) }
	if err := os.WriteFile(filepath.Join(dir, "Inbox.md"), []byte("# 收件箱\n\n#### 28 September, Monday\n已整理"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := postAppend(t, s, `{"id":"x","text":"新的"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	data, _ := os.ReadFile(filepath.Join(dir, "Inbox.md"))
	if want := "# 收件箱\n\n#### 28 September, Monday\n已整理\n\n- [ ] `09:05` 新的\n"; string(data) != want {
		t.Fatalf("got:\n%s", data)
	}
}

func TestAppendRejectsEmptyAndBadInbox(t *testing.T) {
	s, _, _ := fixture(t)
	if w := postAppend(t, s, `{"id":"x","title":"  ","text":"\n\n"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := postAppend(t, s, `{"text":"x"}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	for _, bad := range []string{"../x.md", "/abs.md", "inbox/{YYYY}.txt"} {
		if validInbox(bad) {
			t.Fatal("accepted", bad)
		}
	}
	if !validInbox(defaultInbox) {
		t.Fatal("default rejected")
	}
}

func TestAppendMatchesFilesmdChat(t *testing.T) {
	s, dir, _ := fixture(t)
	chat := "#### 17 September, Thursday\n- [ ] `17:12` 交流群\nGitHub：github.com/x\n#### 24 September, Thursday\n- [ ] `15:58` 我选择用一个单文件程序\n\nhttps://github.com/jijinggang/MarkdownEditor\n"
	if err := os.WriteFile(filepath.Join(dir, "Chat.md"), []byte(chat), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 24, 16, 5, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, req := range []string{
		`{"id":"a","text":"同一天，前一条是多行"}`,
		`{"id":"b","text":"紧跟单行条目"}`,
	} {
		if w := postAppend(t, s, req); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	now = time.Date(2026, 9, 25, 8, 3, 0, 0, time.UTC)
	postAppend(t, s, `{"id":"c","title":"购物","text":"- 牛奶\n- 面包"}`)
	postAppend(t, s, `{"id":"d","text":"![](attachments/a.png)"}`)
	data, _ := os.ReadFile(filepath.Join(dir, "Chat.md"))
	want := chat +
		"\n- [ ] `16:05` 同一天，前一条是多行\n" +
		"- [ ] `16:05` 紧跟单行条目\n" +
		"\n#### 25 September, Friday\n" +
		"- [ ] `08:03` 购物\n- 牛奶\n- 面包\n" +
		"\n- [ ] `08:03` ![](attachments/a.png)\n"
	if string(data) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", data, want)
	}
}

func TestAppendStartsEmptyFile(t *testing.T) {
	got := formatEntry("", time.Date(2026, 9, 5, 18, 4, 0, 0, time.UTC), "", "第一条")
	if want := "#### 5 September, Saturday\n- [ ] `18:04` 第一条\n"; got != want {
		t.Fatalf("got %q", got)
	}
}

func TestAppendToTitledNoteCreatesThenExtends(t *testing.T) {
	s, dir, _ := fixture(t)
	s.now = func() time.Time { return time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC) }
	if w := postAppend(t, s, `{"id":"1","title":"test","text":"第一段","path":"notes/read/test.md"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	postAppend(t, s, `{"id":"2","title":"test","text":"第二段","path":"notes/read/test.md"}`)
	postAppend(t, s, `{"id":"2","title":"test","text":"第二段","path":"notes/read/test.md"}`) // retry
	data, err := os.ReadFile(filepath.Join(dir, "notes", "read", "test.md"))
	if err != nil || string(data) != "第一段\n\n第二段\n" {
		t.Fatalf("%q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Chat.md")); err == nil {
		t.Fatal("titled note also went to the inbox")
	}
	// A title alone creates an empty note; an existing note is left as is.
	postAppend(t, s, `{"id":"3","title":"空","path":"空.md"}`)
	if data, err := os.ReadFile(filepath.Join(dir, "空.md")); err != nil || len(data) != 0 {
		t.Fatalf("%q %v", data, err)
	}
}

func TestAppendNoteSeparatesWithOneBlankLine(t *testing.T) {
	for existing, want := range map[string]string{
		"":         "x\n",
		"a":        "a\n\nx\n",
		"a\n":      "a\n\nx\n",
		"a\n\n":    "a\n\nx\n",
		"  \n":     "x\n",
		"# 标题\n正文": "# 标题\n正文\n\nx\n",
	} {
		if got := appendNote(existing, "x"); got != want {
			t.Errorf("appendNote(%q) = %q, want %q", existing, got, want)
		}
	}
}

func TestAppendRejectsUnsafeNotePaths(t *testing.T) {
	s, dir, _ := fixture(t)
	for i, bad := range []string{"../x.md", "/abs.md", "a/../../x.md", ".git/config.md", "a/.hidden/x.md", "x.txt", "a:b.md", "a?.md", `a\b.md`, "a /b.md", "."} {
		body, _ := json.Marshal(map[string]string{"id": fmt.Sprint("bad", i), "text": "x", "path": bad})
		if w := postAppend(t, s, string(body)); w.Code != 400 {
			t.Errorf("%q accepted: %d %s", bad, w.Code, w.Body.String())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatal("rejected paths wrote files", entries)
	}
}

func TestConfigReportsInboxAndNewDir(t *testing.T) {
	s, _, h := fixture(t)
	s.newDir = "notes/new"
	r := httptest.NewRequest("GET", "/v1/config", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var config map[string]string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &config) != nil ||
		config["inbox"] != defaultInbox || config["newDir"] != "notes/new" {
		t.Fatal(w.Code, w.Body.String())
	}
	for dir, ok := range map[string]bool{"": true, "notes": true, "a/b": true, ".git": false, "../x": false, "a:b": false, "a/./b": false} {
		if validNewDir(dir) != ok {
			t.Errorf("validNewDir(%q) = %v", dir, !ok)
		}
	}
}
