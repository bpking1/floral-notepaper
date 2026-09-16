package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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
	root *os.Root
	mu   sync.Mutex
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

func (s *store) list(ctx context.Context) ([]string, error) {
	files := []string{}
	visited := 0
	err := fs.WalkDir(s.root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > 100000 {
			return errors.New("directory too large; select a smaller notes directory")
		}
		if name != "." && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}
		if entry.Type().IsRegular() && (strings.EqualFold(path.Ext(name), ".md") || strings.EqualFold(path.Ext(name), ".markdown")) {
			files = append(files, name)
			if len(files) > 5000 {
				return errors.New("more than 5000 notes; select a smaller notes directory")
			}
		}
		return nil
	})
	return files, err
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
	lock, err := s.root.Open(".")
	if err != nil {
		return "", err
	}
	defer lock.Close()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			return "", errors.New("notes directory busy")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
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
