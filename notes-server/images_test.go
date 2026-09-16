package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"golang.org/x/image/bmp"
)

func testImage(t *testing.T, format string) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 2, 1))
	picture.Set(0, 0, color.RGBA{255, 0, 0, 255})
	picture.Set(1, 0, color.RGBA{0, 255, 0, 255})
	var out bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&out, picture)
	case "jpg":
		err = jpeg.Encode(&out, picture, nil)
	case "gif":
		frame := image.NewPaletted(picture.Bounds(), color.Palette{color.Black, color.White})
		frame.SetColorIndex(0, 0, 1)
		err = gif.EncodeAll(&out, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{10, 10}})
	case "bmp":
		err = bmp.Encode(&out, picture)
	case "webp":
		// A minimal 1x1 lossy WebP; no external fixture or encoder needed.
		data, decodeErr := base64.StdEncoding.DecodeString("UklGRiIAAABXRUJQVlA4IBYAAAAwAQCdASoBAAEADsD+JaQAA3AAAAAA")
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return data
	default:
		t.Fatal("unknown fixture format", format)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func callImage(h http.Handler, method, name string, data []byte) *httptest.ResponseRecorder {
	route := "/v1/image?path="
	if method == http.MethodPost {
		route = "/v1/images?note="
	}
	r := httptest.NewRequest(method, route+url.QueryEscape(name), bytes.NewReader(data))
	r.Header.Set("Authorization", "Bearer "+testToken)
	// The server must identify actual bytes, independent of client claims.
	r.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestImageRoundTripFormatsAndRelativePaths(t *testing.T) {
	_, dir, h := fixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "日记", "工作"), 0700); err != nil {
		t.Fatal(err)
	}
	note := "日记/工作/today.md"
	original := []byte("# existing note\r\n\r\n")
	if err := os.WriteFile(filepath.Join(dir, note), original, 0640); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"png", "jpg", "gif", "webp", "bmp"} {
		t.Run(format, func(t *testing.T) {
			data := testImage(t, format)
			w := callImage(h, "POST", note, data)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var result uploadedImage
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if path.Dir(result.Path) != "日记/工作/images" || path.Dir(result.MarkdownPath) != "images" || path.Base(result.Path) != path.Base(result.MarkdownPath) || path.Ext(result.Path) != "."+format {
				t.Fatal("unexpected paths", result)
			}
			stored, err := os.ReadFile(filepath.Join(dir, result.Path))
			if err != nil || !bytes.Equal(stored, data) {
				t.Fatal("image bytes changed on disk", err)
			}
			got := callImage(h, "GET", result.Path, nil)
			mime := "image/" + format
			if format == "jpg" {
				mime = "image/jpeg"
			}
			if got.Code != 200 || got.Header().Get("Content-Type") != mime || !bytes.Equal(got.Body.Bytes(), data) {
				t.Fatal("image read changed bytes or MIME", got.Code, got.Header())
			}
			if got.Header().Get("X-Content-Type-Options") != "nosniff" || got.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("missing response headers")
			}
		})
	}
	unchanged, err := os.ReadFile(filepath.Join(dir, note))
	if err != nil || !bytes.Equal(original, unchanged) {
		t.Fatal("upload changed Markdown", err)
	}
	if info, err := os.Stat(filepath.Join(dir, note)); err != nil || info.Mode().Perm() != 0640 {
		t.Fatal("upload changed note permissions", err)
	}
	if w := callImage(h, "POST", "日记/工作/unsaved.md", testImage(t, "png")); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "日记/工作/unsaved.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("upload created unsaved note", err)
	}
	if w := callImage(h, "POST", "missing-parent/unsaved.md", testImage(t, "png")); w.Code != 404 {
		t.Fatal("missing note parent accepted", w.Code)
	}
}

func TestImageExistingAssetsAndContentDetection(t *testing.T) {
	_, dir, h := fixture(t)
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	data := testImage(t, "png")
	// Existing vault images can be outside the upload directory; the extension
	// never determines the response Content-Type.
	if err := os.WriteFile(filepath.Join(dir, "assets", "existing.JPEG"), data, 0600); err != nil {
		t.Fatal(err)
	}
	w := callImage(h, "GET", "assets/existing.JPEG", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatal(w.Code, w.Header())
	}
	for _, invalid := range [][]byte{nil, []byte("# not an image"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), data[:33]} {
		r := httptest.NewRequest("POST", "/v1/images?note=Inbox.md", bytes.NewReader(invalid))
		r.Header.Set("Authorization", "Bearer "+testToken)
		r.Header.Set("Content-Type", "image/png")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 415 || !strings.Contains(w.Body.String(), "unsupportedImage") {
			t.Fatal("accepted false image type", w.Code, w.Body.String())
		}
		if err := os.WriteFile(filepath.Join(dir, "fake.png"), invalid, 0600); err != nil {
			t.Fatal(err)
		}
		if w := callImage(h, "GET", "fake.png", nil); w.Code != 415 {
			t.Fatal("served non-image bytes", w.Code)
		}
	}
}

func TestImageAuthenticationAndConfinement(t *testing.T) {
	_, dir, h := fixture(t)
	data := testImage(t, "png")
	for _, endpoint := range []struct{ method, route string }{{"POST", "/v1/images?note=Inbox.md"}, {"GET", "/v1/image?path=secret.png"}} {
		for _, auth := range []string{"", "Bearer incorrect"} {
			r := httptest.NewRequest(endpoint.method, endpoint.route, bytes.NewReader(data))
			r.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal("image endpoint lacks authentication", endpoint, w.Code)
			}
		}
	}
	outside := t.TempDir()
	for _, name := range []string{"secret.png", "secret.md"} {
		if err := os.WriteFile(filepath.Join(outside, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.png"), filepath.Join(dir, "shortcut.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(dir, "shortcut.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "directory.png"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe.png"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret.png", "/etc/secret.png", "x/../../secret.png", "link/secret.png", "shortcut.png", "x\\secret.png", "x\x00.png", "secret.md", "directory.png", "pipe.png"} {
		if w := callImage(h, "GET", name, nil); w.Code != 400 {
			t.Fatal("invalid image path not rejected", name, w.Code, w.Body.String())
		}
	}
	for _, note := range []string{"../secret.md", "/etc/secret.md", "link/secret.md", "shortcut.md", "x\\secret.md", "secret.png"} {
		if w := callImage(h, "POST", note, data); w.Code != 400 {
			t.Fatal("invalid note path not rejected", note, w.Code, w.Body.String())
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "images")); err != nil {
		t.Fatal(err)
	}
	if w := callImage(h, "POST", "Inbox.md", data); w.Code != 400 {
		t.Fatal("image directory symlink accepted", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 2 {
		t.Fatal("upload escaped vault", entries, err)
	}
}

func TestImageSizeAndPixelLimits(t *testing.T) {
	_, dir, h := fixture(t)
	large := make([]byte, maxImageSize+1)
	if w := callImage(h, "POST", "Inbox.md", large); w.Code != 413 {
		t.Fatal("upload size limit ignored", w.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.png"), large, 0600); err != nil {
		t.Fatal(err)
	}
	if w := callImage(h, "GET", "large.png", nil); w.Code != 413 {
		t.Fatal("read size limit ignored", w.Code)
	}
	pixels := testImage(t, "png")
	// Change the dimensions in a valid IHDR, preserving its checksum. The
	// server must reject it before allocating or decoding pixel data.
	binary.BigEndian.PutUint32(pixels[16:20], 8000)
	binary.BigEndian.PutUint32(pixels[20:24], 8000)
	binary.BigEndian.PutUint32(pixels[29:33], crc32.ChecksumIEEE(pixels[12:29]))
	if w := callImage(h, "POST", "Inbox.md", pixels); w.Code != 413 || !strings.Contains(w.Body.String(), "40 million") {
		t.Fatal("pixel limit ignored", w.Code, w.Body.String())
	}
}

func TestImageDedupConcurrencyAndNoOverwrite(t *testing.T) {
	s, dir, h := fixture(t)
	root2, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root2.Close()
	handlers := []http.Handler{h, handler(&store{root: root2}, testToken)}
	data := testImage(t, "png")
	results := make(chan *httptest.ResponseRecorder, 12)
	var wg sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- callImage(handlers[i%len(handlers)], "POST", "Inbox.md", data)
		}(i)
	}
	wg.Wait()
	close(results)
	imagePath := ""
	for result := range results {
		if result.Code != 200 {
			t.Fatal(result.Code, result.Body.String())
		}
		var uploaded uploadedImage
		if err := json.Unmarshal(result.Body.Bytes(), &uploaded); err != nil {
			t.Fatal(err)
		}
		if imagePath != "" && imagePath != uploaded.Path {
			t.Fatal("retry created duplicate files")
		}
		imagePath = uploaded.Path
	}
	entries, err := os.ReadDir(filepath.Join(dir, "images"))
	if err != nil || len(entries) != 1 || entries[0].Name() != path.Base(imagePath) {
		t.Fatal("duplicate images or temporary files leaked", entries, err)
	}
	before, err := os.Stat(filepath.Join(dir, imagePath))
	if err != nil {
		t.Fatal(err)
	}
	if w := callImage(h, "POST", "Inbox.md", data); w.Code != 200 {
		t.Fatal("lost response retry failed", w.Code)
	}
	after, err := os.Stat(filepath.Join(dir, imagePath))
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("retry rewrote existing image", err)
	}
	if err := os.WriteFile(filepath.Join(dir, imagePath), []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := callImage(h, "POST", "Inbox.md", data); w.Code != 409 {
		t.Fatal("conflicting image accepted", w.Code, w.Body.String())
	}
	preserved, err := os.ReadFile(filepath.Join(dir, imagePath))
	if err != nil || string(preserved) != "external edit" {
		t.Fatal("existing image was overwritten", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.uploadImage(ctx, "Inbox.md", data); !errors.Is(err, context.Canceled) {
		t.Fatal("upload ignored canceled request", err)
	}
}
