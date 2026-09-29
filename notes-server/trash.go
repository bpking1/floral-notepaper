package main

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// trashDir holds deleted notes; hidden, so listings skip it.
const trashDir = ".trash"

// trash moves a note into .trash/, keeping its directory layout, so a
// deletion from the desktop can be undone in the notes directory. With
// expected set, the note must still be at that revision.
func (s *store) trash(ctx context.Context, name, expected string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockDirectory(ctx, s.root)
	if err != nil {
		return "", err
	}
	defer unlock()
	// read validates the path and rejects symlinks and non-Markdown files.
	old, err := s.read(name)
	if err != nil {
		return "", err
	}
	if expected != "" && expected != old.Revision {
		return "", errConflict
	}
	dest := path.Join(trashDir, name)
	if err := s.root.MkdirAll(path.Dir(dest), 0700); err != nil {
		return "", err
	}
	// Keep earlier deletions of the same path.
	if _, err := s.root.Lstat(dest); err == nil {
		ext := path.Ext(dest)
		dest = fmt.Sprintf("%s-%s%s", strings.TrimSuffix(dest, ext), s.clock().Format("20060102-150405"), ext)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.root.Rename(name, dest); err != nil {
		return "", err
	}
	return dest, nil
}
