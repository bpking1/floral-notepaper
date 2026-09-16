package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

type APIServer struct {
	store       *MarkdownStore
	token       []byte
	maxBodySize int64
	logger      *log.Logger
}

type captureRequest struct {
	Content string `json:"content"`
	ID      string `json:"id,omitempty"`
}

type captureResponse struct {
	ID        string `json:"id"`
	CreatedAt string `json:"createdAt"`
	Date      string `json:"date"`
	Time      string `json:"time"`
	Duplicate bool   `json:"duplicate"`
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewAPIServer(store *MarkdownStore, token string, maxBodySize int64, logger *log.Logger) *APIServer {
	return &APIServer{
		store:       store,
		token:       []byte(token),
		maxBodySize: maxBodySize,
		logger:      logger,
	}
}

func (s *APIServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /v1/capture", s.handleCapture)
	return securityHeaders(mux)
}

func (s *APIServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *APIServer) handleCapture(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="capture"`)
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}

	payload, err := s.readCaptureRequest(w, r)
	if err != nil {
		var requestErr *requestError
		if errors.As(err, &requestErr) {
			writeError(w, requestErr.status, requestErr.code, requestErr.message)
			return
		}
		s.logger.Printf("read capture request: %v", err)
		writeError(w, http.StatusBadRequest, "invalidRequest", "invalid request")
		return
	}

	headerID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if headerID != "" && payload.ID != "" && headerID != payload.ID {
		writeError(w, http.StatusBadRequest, "idMismatch", "Idempotency-Key and JSON id must match")
		return
	}
	id := headerID
	if id == "" {
		id = payload.ID
	}
	if id == "" {
		id, err = randomID()
		if err != nil {
			s.logger.Printf("generate capture id: %v", err)
			writeError(w, http.StatusInternalServerError, "internal", "could not create capture")
			return
		}
	}
	if !idPattern.MatchString(id) {
		writeError(w, http.StatusBadRequest, "invalidId", "idempotency key must use 1-128 letters, numbers, dot, underscore, colon or hyphen")
		return
	}
	if strings.TrimSpace(payload.Content) == "" {
		writeError(w, http.StatusUnprocessableEntity, "emptyContent", "content must not be empty")
		return
	}
	if !utf8.ValidString(payload.Content) {
		writeError(w, http.StatusBadRequest, "invalidEncoding", "content must be valid UTF-8")
		return
	}

	result, err := s.store.Append(id, payload.Content)
	if err != nil {
		switch {
		case errors.Is(err, ErrIdempotencyConflict):
			writeError(w, http.StatusConflict, "idempotencyConflict", err.Error())
		case errors.Is(err, ErrReservedMarker):
			writeError(w, http.StatusUnprocessableEntity, "reservedMarker", err.Error())
		case errors.Is(err, ErrLockTimeout):
			writeError(w, http.StatusServiceUnavailable, "lockTimeout", "capture file is busy; retry later")
		default:
			s.logger.Printf("append capture: %v", err)
			writeError(w, http.StatusInternalServerError, "writeFailed", "could not append capture")
		}
		return
	}

	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, captureResponse{
		ID:        result.ID,
		CreatedAt: result.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		Date:      result.CreatedAt.Format("2006-01-02"),
		Time:      result.CreatedAt.Format("15:04:05"),
		Duplicate: result.Duplicate,
	})
}

func (s *APIServer) readCaptureRequest(w http.ResponseWriter, r *http.Request) (captureRequest, error) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodySize)
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return captureRequest{}, newRequestError(http.StatusUnsupportedMediaType, "unsupportedMediaType", "Content-Type must be text/markdown, text/plain or application/json")
	}
	if charset := parameters["charset"]; charset != "" && !strings.EqualFold(charset, "utf-8") {
		return captureRequest{}, newRequestError(http.StatusUnsupportedMediaType, "unsupportedCharset", "only UTF-8 request bodies are supported")
	}

	switch mediaType {
	case "text/markdown", "text/plain":
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return captureRequest{}, mapBodyReadError(err)
		}
		return captureRequest{Content: string(body)}, nil
	case "application/json":
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var payload captureRequest
		if err := decoder.Decode(&payload); err != nil {
			return captureRequest{}, mapBodyReadError(err)
		}
		if err := ensureJSONEOF(decoder); err != nil {
			return captureRequest{}, err
		}
		return payload, nil
	default:
		return captureRequest{}, newRequestError(http.StatusUnsupportedMediaType, "unsupportedMediaType", "Content-Type must be text/markdown, text/plain or application/json")
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return newRequestError(http.StatusBadRequest, "invalidJSON", "request body must contain exactly one JSON object")
	}
	return nil
}

func mapBodyReadError(err error) error {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return newRequestError(http.StatusRequestEntityTooLarge, "bodyTooLarge", "request body is too large")
	}
	return newRequestError(http.StatusBadRequest, "invalidBody", "could not read request body")
}

func (s *APIServer) authorized(header string) bool {
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	candidate := []byte(strings.TrimSpace(value))
	if len(candidate) != len(s.token) {
		return false
	}
	return subtle.ConstantTimeCompare(candidate, s.token) == 1
}

func randomID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

type requestError struct {
	status  int
	code    string
	message string
}

func (e *requestError) Error() string {
	return fmt.Sprintf("%s: %s", e.code, e.message)
}

func newRequestError(status int, code, message string) error {
	return &requestError{status: status, code: code, message: message}
}
