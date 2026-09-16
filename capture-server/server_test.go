package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

func TestCaptureAPIAcceptsMarkdownAndDeduplicatesRetry(t *testing.T) {
	handler, path := newTestHandler(t, 1024)
	body := "这是 **Markdown**。\n\n- [ ] 测试"
	request := captureRequestForTest(t, body, "text/markdown", "request-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body=%s", response.Code, response.Body.String())
	}
	var first captureResponse
	decodeResponse(t, response.Body, &first)
	if first.Duplicate || first.ID != "request-1" || first.Date != "2026-08-31" {
		t.Fatalf("unexpected first response: %+v", first)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("Cache-Control: no-store was not set")
	}

	retry := captureRequestForTest(t, body, "text/markdown", "request-1")
	retryResponse := httptest.NewRecorder()
	handler.ServeHTTP(retryResponse, retry)
	if retryResponse.Code != http.StatusOK {
		t.Fatalf("retry status = %d, body=%s", retryResponse.Code, retryResponse.Body.String())
	}
	var duplicate captureResponse
	decodeResponse(t, retryResponse.Body, &duplicate)
	if !duplicate.Duplicate {
		t.Fatal("retry was not reported as duplicate")
	}
	if count := strings.Count(readFile(t, path), body); count != 1 {
		t.Fatalf("captured body count = %d, want 1", count)
	}
}

func TestCaptureAPIAcceptsJSON(t *testing.T) {
	handler, path := newTestHandler(t, 1024)
	body := `{"id":"json-id","content":"# 标题\n\n正文"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(readFile(t, path), "# 标题\n\n正文") {
		t.Fatal("JSON Markdown content was not preserved")
	}
}

func TestCaptureAPIRejectsUnauthorizedRequest(t *testing.T) {
	handler, _ := newTestHandler(t, 1024)
	request := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader("secret note"))
	request.Header.Set("Content-Type", "text/markdown")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if response.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("WWW-Authenticate header was not set")
	}
}

func TestCaptureAPIRejectsIdempotencyConflict(t *testing.T) {
	handler, _ := newTestHandler(t, 1024)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, captureRequestForTest(t, "first", "text/plain", "same"))
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d", first.Code)
	}

	conflict := httptest.NewRecorder()
	handler.ServeHTTP(conflict, captureRequestForTest(t, "second", "text/plain", "same"))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestCaptureAPIValidatesInput(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		id          string
		maxBody     int64
		wantStatus  int
	}{
		{name: "unsupported content type", contentType: "application/x-www-form-urlencoded", body: []byte("note"), id: "id", maxBody: 1024, wantStatus: http.StatusUnsupportedMediaType},
		{name: "unsupported charset", contentType: "text/plain; charset=iso-8859-1", body: []byte("note"), id: "id", maxBody: 1024, wantStatus: http.StatusUnsupportedMediaType},
		{name: "empty content", contentType: "text/plain", body: []byte(" \n\t"), id: "id", maxBody: 1024, wantStatus: http.StatusUnprocessableEntity},
		{name: "invalid utf8", contentType: "text/plain", body: []byte{0xff, 0xfe}, id: "id", maxBody: 1024, wantStatus: http.StatusBadRequest},
		{name: "body too large", contentType: "text/plain", body: []byte("123456"), id: "id", maxBody: 5, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "invalid id", contentType: "text/plain", body: []byte("note"), id: "contains spaces", maxBody: 1024, wantStatus: http.StatusBadRequest},
		{name: "reserved marker", contentType: "text/plain", body: []byte("<!-- floral-capture-entry:v1 -->"), id: "id", maxBody: 1024, wantStatus: http.StatusUnprocessableEntity},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := newTestHandler(t, test.maxBody)
			request := httptest.NewRequest(http.MethodPost, "/v1/capture", bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+testToken)
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Idempotency-Key", test.id)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", response.Code, test.wantStatus, response.Body.String())
			}
		})
	}
}

func TestCaptureAPIRejectsMismatchedJSONAndHeaderIDs(t *testing.T) {
	handler, _ := newTestHandler(t, 1024)
	request := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader(`{"id":"json-id","content":"note"}`))
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "header-id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func TestCaptureAPIGeneratesID(t *testing.T) {
	handler, _ := newTestHandler(t, 1024)
	request := captureRequestForTest(t, "note", "text/plain", "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var payload captureResponse
	decodeResponse(t, response.Body, &payload)
	if len(payload.ID) != 32 || !idPattern.MatchString(payload.ID) {
		t.Fatalf("generated ID = %q", payload.ID)
	}
}

func TestHealthEndpointIsAnonymous(t *testing.T) {
	handler, _ := newTestHandler(t, 1024)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if strings.Contains(response.Body.String(), "Inbox") {
		t.Fatal("health response leaked target information")
	}
}

func newTestHandler(t *testing.T, maxBody int64) (http.Handler, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Inbox.md")
	store := NewMarkdownStore(path, time.UTC, time.Second)
	store.now = func() time.Time { return time.Date(2026, 8, 31, 21, 35, 42, 0, time.UTC) }
	logger := log.New(io.Discard, "", 0)
	return NewAPIServer(store, testToken, maxBody, logger).Handler(), path
}

func captureRequestForTest(t *testing.T, body, contentType, id string) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/capture", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testToken)
	request.Header.Set("Content-Type", contentType)
	if id != "" {
		request.Header.Set("Idempotency-Key", id)
	}
	return request
}

func decodeResponse(t *testing.T, reader io.Reader, target any) {
	t.Helper()
	if err := json.NewDecoder(reader).Decode(target); err != nil {
		t.Fatal(err)
	}
}
