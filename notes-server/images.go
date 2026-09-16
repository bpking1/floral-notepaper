package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
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

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

const (
	maxImageSize   = 20 << 20
	maxImagePixels = 40_000_000
)

var (
	errImagePath     = errors.New("use a relative PNG, JPEG, GIF, WebP or BMP path without symlinks")
	errImageLarge    = errors.New("image exceeds 20 MiB")
	errImageFormat   = errors.New("invalid or unsupported image; use PNG, JPEG, GIF, static WebP or BMP (SVG and animated WebP are not supported)")
	errImagePixels   = errors.New("image exceeds 40 million pixels")
	errImageConflict = errors.New("an existing image at this content-addressed path has different contents")
	// Decode only one image at a time, bounding memory even for compressed images.
	imageDecodeMu sync.Mutex
)

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

func (s *store) uploadImage(ctx context.Context, note string, data []byte) (uploadedImage, error) {
	if err := s.validate(note); err != nil {
		return uploadedImage{}, err
	}
	extension, _, err := inspectImage(data)
	if err != nil {
		return uploadedImage{}, err
	}
	if err := ctx.Err(); err != nil {
		return uploadedImage{}, err
	}
	parent, err := openImageDirectory(s.root, path.Dir(note))
	if err != nil {
		return uploadedImage{}, err
	}
	defer parent.Close()
	if err := parent.Mkdir("images", 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return uploadedImage{}, err
	}
	images, err := openImageDirectory(parent, "images")
	if err != nil {
		return uploadedImage{}, err
	}
	defer images.Close()
	// Sync even if another API process created this directory concurrently.
	if err := syncImageDirectory(parent); err != nil {
		return uploadedImage{}, err
	}
	name := fmt.Sprintf("%x.%s", sha256.Sum256(data), extension)
	relative := path.Join("images", name)
	result := uploadedImage{Path: path.Join(path.Dir(note), relative), MarkdownPath: relative}
	if existing, err := imageBytes(images, name); err == nil {
		if !bytes.Equal(existing, data) {
			return uploadedImage{}, errImageConflict
		}
		return result, syncImageDirectory(images)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return uploadedImage{}, err
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
	// Atomic create-if-absent works across API processes and never replaces an
	// existing image, including files changed by external editors.
	if err := images.Link(temp, name); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return uploadedImage{}, err
		}
		existing, err := imageBytes(images, name)
		if err != nil {
			return uploadedImage{}, err
		}
		if !bytes.Equal(existing, data) {
			return uploadedImage{}, errImageConflict
		}
	}
	if err := images.Remove(temp); err != nil {
		return uploadedImage{}, err
	}
	return result, syncImageDirectory(images)
}
