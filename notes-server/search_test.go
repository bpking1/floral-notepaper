package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func callSearch(h http.Handler, rawQuery string, extra string, acceptGzip bool) *httptest.ResponseRecorder {
	target := "/v1/search?q=" + url.QueryEscape(rawQuery)
	if extra != "" {
		target += "&" + extra
	}
	r := httptest.NewRequest(http.MethodGet, target, nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	if acceptGzip {
		r.Header.Set("Accept-Encoding", "gzip")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestSearchEndpointAndFiltering(t *testing.T) {
	_, dir, h := fixture(t)

	if err := os.MkdirAll(filepath.Join(dir, "日记"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".trash"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Chat.md"), []byte("#### 20 November\n- [ ] 晚上买咖啡豆\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "日记/随笔.md"), []byte("# 随笔\n上午喝了一杯手冲咖啡\n准备写方案\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".trash/deleted.md"), []byte("这里也有咖啡"), 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, "Chat.md"), now.Add(-time.Hour), now.Add(-time.Hour))
	_ = os.Chtimes(filepath.Join(dir, "日记/随笔.md"), now, now)

	// 鉴权校验
	unauthReq := httptest.NewRequest(http.MethodGet, "/v1/search?q=咖啡", nil)
	unauthRec := httptest.NewRecorder()
	h.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != 401 {
		t.Fatalf("expected 401 without auth, got %d", unauthRec.Code)
	}

	// 非法空查询或非法 limit
	if w := callSearch(h, "   ", "", false); w.Code != 400 {
		t.Fatalf("expected 400 for empty query, got %d", w.Code)
	}
	if w := callSearch(h, "咖啡", "limit=abc", false); w.Code != 400 {
		t.Fatalf("expected 400 for bad limit, got %d", w.Code)
	}

	// 全库搜索应排除 .trash 且按最新修改排序
	w := callSearch(h, "咖啡", "", true)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("unexpected gzip search status=%d encoding=%q", w.Code, w.Header().Get("Content-Encoding"))
	}
	gz, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	body, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	var res searchResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || res.Truncated {
		t.Fatalf("unexpected search results: %#v", res)
	}
	if res.Results[0].Path != "日记/随笔.md" || res.Results[0].Line != 2 {
		t.Fatalf("expected newest file first with line 2, got %#v", res.Results[0])
	}
	if !strings.Contains(res.Results[0].Snippet, "咖啡") {
		t.Fatalf("missing snippet highlight context: %#v", res.Results[0])
	}

	// limit=1 触发 truncated=true
	wLim := callSearch(h, "咖啡", "limit=1", false)
	var limRes searchResponse
	if err := json.Unmarshal(wLim.Body.Bytes(), &limRes); err != nil {
		t.Fatal(err)
	}
	if len(limRes.Results) != 1 || !limRes.Truncated {
		t.Fatalf("expected 1 truncated result, got %#v", limRes)
	}
}
