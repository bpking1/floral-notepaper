package main

import (
	"compress/gzip"
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

type gzipResponseWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (w gzipResponseWriter) Write(b []byte) (int, error) { return w.gz.Write(b) }

func apiError(w http.ResponseWriter, status int, code, message string) {
	jsonResponse(w, status, map[string]string{"code": code, "message": message})
}
func storeError(w http.ResponseWriter, err error) {
	var status int
	var code, message string
	switch {
	case errors.Is(err, errConflict):
		status, code, message = 412, "conflict", err.Error()
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
	case errors.Is(err, errAppendEmpty), errors.Is(err, errAppendID), errors.Is(err, errAppendPath):
		status, code, message = 400, "invalidEntry", err.Error()
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
	// Lets clients show where a capture will go before sending it.
	mux.HandleFunc("GET /v1/config", func(w http.ResponseWriter, r *http.Request) {
		inbox := s.inbox
		if inbox == "" {
			inbox = defaultInbox
		}
		jsonResponse(w, 200, map[string]string{"inbox": inbox, "newDir": s.newDir})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("POST /v1/images", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageSize))
		if err != nil {
			apiError(w, 413, "tooLarge", "image body exceeds 20 MiB or could not be read")
			return
		}
		var result uploadedImage
		query := r.URL.Query()
		if query.Has("inbox") {
			result, err = s.uploadInboxImage(r.Context(), query.Get("name"), data)
		} else {
			result, err = s.uploadImage(r.Context(), query.Get("note"), query.Get("name"), data)
		}
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
		files, truncated, err := s.list(r.Context())
		if err != nil {
			storeError(w, err)
			return
		}
		body := map[string]any{"files": files, "truncated": truncated}
		// Listings of large libraries are several hundred KB; paths compress well.
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			jsonResponse(w, 200, body)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		jsonResponse(gzipResponseWriter{w, gz}, 200, body)
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
	mux.HandleFunc("DELETE /v1/file", func(w http.ResponseWriter, r *http.Request) {
		trashedTo, err := s.trash(r.Context(), r.URL.Query().Get("path"), r.Header.Get("If-Match"))
		if err != nil {
			storeError(w, err)
			return
		}
		jsonResponse(w, 200, map[string]string{"trashedTo": trashedTo})
	})
	mux.HandleFunc("POST /v1/append", func(w http.ResponseWriter, r *http.Request) {
		var req appendRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFileSize)).Decode(&req); err != nil {
			apiError(w, 400, "invalidEntry", "body must be JSON {id, title, text, path} under 2 MiB")
			return
		}
		result, err := s.append(r.Context(), req)
		if err != nil {
			storeError(w, err)
			return
		}
		jsonResponse(w, 200, result)
	})
	expected := sha256.Sum256([]byte("Bearer " + token))
	return requestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Native desktop client uses no cookies and does not need CORS.
		publicImage := s.publicImages && r.Method == http.MethodGet && r.URL.Path == "/v1/image"
		if publicImage {
			// Lets tiles render and copy images straight from the LAN server.
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if strings.HasPrefix(r.URL.Path, "/v1/") {
			actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
			if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
				apiError(w, 401, "unauthorized", "valid bearer token required")
				return
			}
		}
		mux.ServeHTTP(w, r)
	}))
}
