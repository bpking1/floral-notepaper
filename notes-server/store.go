package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const maxFileSize = 2 << 20

var (
	errConflict = errors.New("file changed on server; reload and merge before saving")
	errPath     = errors.New("use a relative .md or .markdown path without symlinks")
	errLarge    = errors.New("file exceeds 2 MiB")
	errEncoding = errors.New("file must be UTF-8")
)

type store struct {
	root        *os.Root
	mu          sync.Mutex
	inbox       string // append target pattern; empty means defaultInbox
	attachments string // uploaded image directory; empty means defaultAttachments
	newDir      string // where titled captures without a leading / go; empty means the root
	// publicImages serves GET /v1/image without the token, for trusted networks.
	publicImages bool
	now          func() time.Time // test clock; nil means time.Now

	appendMu    sync.Mutex
	appended    map[string]appendResult
	appendOrder []string
}
type document struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
}

func version(data []byte) string { return fmt.Sprintf(`"%x"`, sha256.Sum256(data)) }

func (s *store) validate(name string) error {
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") || (strings.ToLower(path.Ext(name)) != ".md" && strings.ToLower(path.Ext(name)) != ".markdown") {
		return errPath
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err := s.root.Lstat(strings.Join(parts[:i+1], "/"))
		if errors.Is(err, fs.ErrNotExist) && i == len(parts)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errPath
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return errPath
		}
	}
	return nil
}

func (s *store) read(name string) (document, error) {
	if err := s.validate(name); err != nil {
		return document{}, err
	}
	f, err := s.root.Open(name)
	if err != nil {
		return document{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return document{}, err
	}
	if !info.Mode().IsRegular() {
		return document{}, errPath
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return document{}, err
	}
	if len(data) > maxFileSize {
		return document{}, errLarge
	}
	if !utf8.Valid(data) {
		return document{}, errEncoding
	}
	return document{string(data), version(data)}, nil
}

// maxListedFiles bounds the /v1/files response; larger libraries list the
// most recently modified files and set truncated.
var maxListedFiles = 20000

type fileEntry struct {
	Path     string `json:"path"`
	Modified int64  `json:"modified"` // Unix seconds
}

func (s *store) list(ctx context.Context) (files []fileEntry, truncated bool, err error) {
	files = []fileEntry{}
	visited := 0
	err = fs.WalkDir(s.root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 200000 {
			return errors.New("directory too large; select a smaller notes directory")
		}
		if name != "." && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() && (strings.EqualFold(path.Ext(name), ".md") || strings.EqualFold(path.Ext(name), ".markdown")) {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			files = append(files, fileEntry{Path: name, Modified: info.ModTime().Unix()})
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if len(files) > maxListedFiles {
		slices.SortStableFunc(files, func(a, b fileEntry) int { return cmp.Compare(b.Modified, a.Modified) })
		files, truncated = files[:maxListedFiles], true
	}
	return files, truncated, nil
}

// lockDirectory takes an exclusive flock on dir, shared by cooperating API
// processes, waiting up to 3 seconds.
func lockDirectory(ctx context.Context, dir *os.Root) (func(), error) {
	lock, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			lock.Close()
			return nil, errors.New("notes directory busy")
		}
		select {
		case <-ctx.Done():
			lock.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}, nil
}

func (s *store) write(ctx context.Context, name string, data []byte, expected string, create bool) (string, error) {
	if len(data) > maxFileSize {
		return "", errLarge
	}
	if !utf8.Valid(data) {
		return "", errEncoding
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Cooperating API instances share a directory lock; external editors do not.
	unlock, err := lockDirectory(ctx, s.root)
	if err != nil {
		return "", err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	old, err := s.read(name)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if (create && exists) || (!create && (!exists || expected != old.Revision)) {
		// Retry after a lost response: acknowledge identical content without rewriting.
		if exists && bytes.Equal([]byte(old.Content), data) {
			return old.Revision, nil
		}
		return "", errConflict
	}
	// Anchor operations to the same parent directory, even if it is renamed.
	parent, err := s.root.OpenRoot(path.Dir(name))
	if err != nil {
		return "", err
	}
	defer parent.Close()
	mode := fs.FileMode(0600)
	if exists {
		info, err := parent.Stat(path.Base(name))
		if err != nil {
			return "", err
		}
		mode = info.Mode().Perm()
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	temp := fmt.Sprintf(".floral-%x.tmp", id)
	f, err := parent.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return "", err
	}
	defer parent.Remove(temp)
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Chmod(mode)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	latest, latestErr := s.read(name)
	if (exists && (latestErr != nil || latest.Revision != old.Revision)) || (!exists && !errors.Is(latestErr, fs.ErrNotExist)) {
		return "", errConflict
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if create {
		// Link gives atomic create-if-absent; never overwrite an external new file.
		err = parent.Link(temp, path.Base(name))
		if errors.Is(err, fs.ErrExist) {
			return "", errConflict
		}
	} else {
		err = parent.Rename(temp, path.Base(name))
	}
	if err != nil {
		return "", err
	}
	dir, err := parent.Open(".")
	if err != nil {
		return "", err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		return "", err
	}
	return version(data), nil
}
