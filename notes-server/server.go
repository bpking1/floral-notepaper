package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"
)

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, map[string]string{"code": code, "message": message})
}
func storeError(w http.ResponseWriter, err error) {
	var status int
	var code, message string
	switch {
	case errors.Is(err, errConflict):
		status, code, message = 412, "conflict", err.Error()
	case errors.Is(err, errImageConflict):
		status, code, message = 409, "imageConflict", err.Error()
	case errors.Is(err, fs.ErrNotExist):
		status, code, message = 404, "notFound", "file or parent directory does not exist"
	case errors.Is(err, errPath), errors.Is(err, errImagePath):
		status, code, message = 400, "invalidPath", err.Error()
	case errors.Is(err, errLarge), errors.Is(err, errImageLarge), errors.Is(err, errImagePixels):
		status, code, message = 413, "tooLarge", err.Error()
	case errors.Is(err, errImageFormat):
		status, code, message = 415, "unsupportedImage", err.Error()
	case errors.Is(err, errEncoding):
		status, code, message = 422, "encoding", err.Error()
	case errors.Is(err, fs.ErrPermission):
		status, code, message = 403, "permission", "notes directory permission denied"
	default:
		status, code, message = 500, "io", "file operation failed; check server permissions and directory size"
	}
	if status >= 500 {
		// Server-side failures are invisible in the generic response body; log them.
		log.Printf("store error: %v", err)
	}
	apiError(w, status, code, message)
}

// statusWriter records the response status for request logging.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

// requestLog lines contain method, path and query, but never headers,
// bodies or tokens.
func requestLog(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusWriter{ResponseWriter: w}
		handler.ServeHTTP(recorder, r)
		if recorder.status == 0 {
			recorder.status = http.StatusOK
		}
		log.Printf("%s %s -> %d (%s) from %s", r.Method, r.URL.RequestURI(), recorder.status,
			time.Since(start).Round(time.Microsecond), r.RemoteAddr)
	})
}
func handler(s *store, token string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/images", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageSize))
		if err != nil {
			apiError(w, 413, "tooLarge", "image body exceeds 20 MiB or could not be read")
			return
		}
		result, err := s.uploadImage(r.Context(), r.URL.Query().Get("note"), data)
		if err != nil {
			storeError(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
	mux.HandleFunc("GET /v1/image", func(w http.ResponseWriter, r *http.Request) {
		data, contentType, err := s.readImage(r.URL.Query().Get("path"))
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(200)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /v1/files", func(w http.ResponseWriter, r *http.Request) {
		files, err := s.list(r.Context())
		if err != nil {
			storeError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]any{"files": files})
	})
	mux.HandleFunc("GET /v1/file", func(w http.ResponseWriter, r *http.Request) {
		doc, err := s.read(r.URL.Query().Get("path"))
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("ETag", doc.Revision)
		jsonResponse(w, 200, doc)
	})
	mux.HandleFunc("PUT /v1/file", func(w http.ResponseWriter, r *http.Request) {
		expected, none := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
		create := none == "*"
		if (expected == "" && !create) || (expected != "" && none != "") || (none != "" && !create) {
			apiError(w, 428, "preconditionRequired", "send If-Match from the read response, or If-None-Match: * to create")
			return
		}
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFileSize))
		if err != nil {
			apiError(w, 413, "tooLarge", "body exceeds 2 MiB or could not be read")
			return
		}
		revision, err := s.write(r.Context(), r.URL.Query().Get("path"), data, expected, create)
		if err != nil {
			storeError(w, err)
			return
		}
		w.Header().Set("ETag", revision)
		jsonResponse(w, 200, map[string]string{"revision": revision})
	})
	expected := sha256.Sum256([]byte("Bearer " + token))
	return requestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Native desktop client uses no cookies and does not need CORS.
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
			if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
				apiError(w, 401, "unauthorized", "valid bearer token required")
				return
			}
		}
		mux.ServeHTTP(w, r)
	}))
}
