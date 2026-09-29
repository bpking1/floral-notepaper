package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	defaultAttachments = "media"
	maxImageSize       = 20 << 20
	maxImagePixels     = 40_000_000
)

var (
	errImagePath   = errors.New("use a relative PNG, JPEG, GIF, WebP or BMP path without symlinks")
	errImageLarge  = errors.New("image exceeds 20 MiB")
	errImageFormat = errors.New("invalid or unsupported image; use PNG, JPEG, GIF, static WebP or BMP (SVG and animated WebP are not supported)")
	errImagePixels = errors.New("image exceeds 40 million pixels")
	// Decode only one image at a time, bounding memory even for compressed images.
	imageDecodeMu sync.Mutex
)

func (s *store) attachmentsDir() string {
	if s.attachments == "" {
		return defaultAttachments
	}
	return s.attachments
}

// validAttachments reports whether dir is a usable attachments directory path.
func validAttachments(dir string) bool {
	return dir != "." && fs.ValidPath(dir) && !strings.ContainsAny(dir, "\\\x00")
}

type uploadedImage struct {
	Path         string `json:"path"`
	MarkdownPath string `json:"markdownPath"`
}

func inspectImage(data []byte) (extension, contentType string, err error) {
	if len(data) > maxImageSize {
		return "", "", errImageLarge
	}
	imageDecodeMu.Lock()
	defer imageDecodeMu.Unlock()
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", "", errImageFormat
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > maxImagePixels/config.Height {
		return "", "", errImagePixels
	}
	switch format {
	case "png":
		extension, contentType = "png", "image/png"
	case "jpeg":
		extension, contentType = "jpg", "image/jpeg"
	case "gif":
		extension, contentType = "gif", "image/gif"
	case "webp":
		extension, contentType = "webp", "image/webp"
	case "bmp":
		extension, contentType = "bmp", "image/bmp"
	default:
		return "", "", errImageFormat
	}
	// Validate pixel data as well as the header. GIF decodes its first frame;
	// preserve the complete original file so animations continue to work.
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", "", errImageFormat
	}
	return extension, contentType, nil
}

// openImageDirectory walks directories without accepting symbolic links and
// keeps each operation anchored to the directory checked by Lstat.
func openImageDirectory(root *os.Root, name string) (*os.Root, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
		return nil, errImagePath
	}
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if name == "." {
		return current, nil
	}
	for _, part := range strings.Split(name, "/") {
		info, err := current.Lstat(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, errImagePath
		}
		next, err := current.OpenRoot(part)
		current.Close()
		if err != nil {
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, errImagePath
		}
		current = next
	}
	return current, nil
}

func imageBytes(parent *os.Root, name string) ([]byte, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errImagePath
	}
	// Nonblocking avoids hanging if an external writer replaces a file with a
	// FIFO. O_NOFOLLOW also rejects symlinks introduced after directory checks.
	f, err := parent.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, errImagePath
		}
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) {
		return nil, errImagePath
	}
	if info.Size() > maxImageSize {
		return nil, errImageLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, maxImageSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageSize {
		return nil, errImageLarge
	}
	return data, nil
}

func (s *store) readImage(name string) ([]byte, string, error) {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
		return nil, "", errImagePath
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
	default:
		return nil, "", errImagePath
	}
	parent, err := openImageDirectory(s.root, path.Dir(name))
	if err != nil {
		return nil, "", err
	}
	defer parent.Close()
	data, err := imageBytes(parent, path.Base(name))
	if err != nil {
		return nil, "", err
	}
	_, contentType, err := inspectImage(data)
	return data, contentType, err
}

func syncImageDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// relativePath returns target as a Markdown link from a file in dir; both
// are clean slash-separated paths relative to the notes root.
func relativePath(dir, target string) string {
	if dir == "." {
		return target
	}
	from, to := strings.Split(dir, "/"), strings.Split(target, "/")
	common := 0
	for common < len(from) && common < len(to)-1 && from[common] == to[common] {
		common++
	}
	return strings.Repeat("../", len(from)-common) + strings.Join(to[common:], "/")
}

// uploadImage stores an image linked from note, which may not exist yet, even
// in directories that do not exist yet (a titled capture creates them).
func (s *store) uploadImage(ctx context.Context, note, hint string, data []byte) (uploadedImage, error) {
	if err := s.validate(note); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return uploadedImage{}, err
	}
	return s.storeImage(ctx, note, hint, data)
}

// uploadInboxImage stores an image linked from the inbox file the next append
// will write to, which may not exist yet.
func (s *store) uploadInboxImage(ctx context.Context, hint string, data []byte) (uploadedImage, error) {
	return s.storeImage(ctx, s.inboxPath(s.clock()), hint, data)
}

// storeImage saves data in the shared attachments directory, named after hint
// or the upload time, and links it relative to note. The note is not touched.
func (s *store) storeImage(ctx context.Context, note, hint string, data []byte) (uploadedImage, error) {
	extension, _, err := inspectImage(data)
	if err != nil {
		return uploadedImage{}, err
	}
	if err := ctx.Err(); err != nil {
		return uploadedImage{}, err
	}
	dir := s.attachmentsDir()
	parent, err := openImageDirectory(s.root, path.Dir(dir))
	if errors.Is(err, fs.ErrNotExist) {
		if err := s.root.MkdirAll(path.Dir(dir), 0700); err != nil {
			return uploadedImage{}, err
		}
		parent, err = openImageDirectory(s.root, path.Dir(dir))
	}
	if err != nil {
		return uploadedImage{}, err
	}
	defer parent.Close()
	if err := parent.Mkdir(path.Base(dir), 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return uploadedImage{}, err
	}
	images, err := openImageDirectory(parent, path.Base(dir))
	if err != nil {
		return uploadedImage{}, err
	}
	defer images.Close()
	// Sync even if another API process created this directory concurrently.
	if err := syncImageDirectory(parent); err != nil {
		return uploadedImage{}, err
	}
	// Serialize lookup and creation across API processes so concurrent
	// uploads of the same image end up as one file.
	unlock, err := lockDirectory(ctx, images)
	if err != nil {
		return uploadedImage{}, err
	}
	defer unlock()
	link := func(name string) uploadedImage {
		stored := path.Join(dir, name)
		return uploadedImage{Path: stored, MarkdownPath: relativePath(path.Dir(note), stored)}
	}
	// Reuse an identical stored image: a retry after a lost response, or the
	// same picture pasted twice.
	if name, err := findImage(images, data); err != nil {
		return uploadedImage{}, err
	} else if name != "" {
		return link(name), nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return uploadedImage{}, err
	}
	temp := fmt.Sprintf(".floral-image-%x.tmp", id)
	f, err := images.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return uploadedImage{}, err
	}
	defer images.Remove(temp)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return uploadedImage{}, writeErr
	}
	if closeErr != nil {
		return uploadedImage{}, closeErr
	}
	if err := ctx.Err(); err != nil {
		return uploadedImage{}, err
	}
	base := imageBaseName(hint, s.clock())
	for i := 1; i <= 1000; i++ {
		name := base + "." + extension
		if i > 1 {
			name = fmt.Sprintf("%s-%d.%s", base, i, extension)
		}
		// Link never replaces an existing file, including ones external editors
		// created after the lookup above.
		err := images.Link(temp, name)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return uploadedImage{}, err
		}
		if err := images.Remove(temp); err != nil {
			return uploadedImage{}, err
		}
		return link(name), syncImageDirectory(images)
	}
	return uploadedImage{}, errors.New("too many images with the same name")
}

// findImage returns the name of a stored file with exactly data's bytes, or "".
func findImage(dir *os.Root, data []byte) (string, error) {
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Size() != int64(len(data)) {
			continue
		}
		if existing, err := imageBytes(dir, entry.Name()); err == nil && bytes.Equal(existing, data) {
			return entry.Name(), nil
		}
	}
	return "", nil
}

// imageBaseName turns a client file name into a safe Markdown link target
// without extension, falling back to the upload time, e.g. 20260928-143205.
func imageBaseName(hint string, now time.Time) string {
	name := path.Base(strings.ReplaceAll(hint, "\\", "/"))
	name = strings.TrimSuffix(name, path.Ext(name))
	name = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`/\\:*?"<>|#%[]()^`, r) {
			return '-'
		}
		return r
	}, name)
	if runes := []rune(name); len(runes) > 60 {
		name = string(runes[:60])
	}
	name = strings.Trim(name, ".-")
	if name == "" {
		return now.Format("20060102-150405")
	}
	return name
}
