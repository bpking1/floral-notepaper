package main

import (
	"cmp"
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	defaultSearchLimit  = 50
	maxSearchLimit      = 200
	maxSearchQueryRunes = 200
)

var errQuery = errors.New("search query q must be non-empty and under 200 characters")

func (s *store) readSearchCandidate(name string) (string, bool, error) {
	f, err := s.root.Open(name)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > maxFileSize || !utf8.Valid(data) {
		return "", false, nil
	}
	return string(data), true, nil
}

func (s *store) search(ctx context.Context, rawQuery string, limit int) (searchResponse, error) {
	trimmed := strings.TrimSpace(rawQuery)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > maxSearchQueryRunes {
		return searchResponse{}, errQuery
	}
	terms := splitSearchTerms(trimmed)
	if len(terms) == 0 {
		return searchResponse{}, errQuery
	}

	if limit <= 0 {
		limit = defaultSearchLimit
	} else if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	hits := []searchHit{}
	visited := 0
	err := fs.WalkDir(s.root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(path.Ext(name))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if info.Size() > maxFileSize {
			return nil
		}
		content, ok, err := s.readSearchCandidate(name)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !ok {
			return nil
		}
		matched, line, snippet, matchCount := matchDocument(name, content, terms)
		if matched {
			hits = append(hits, searchHit{
				Path:       name,
				Modified:   info.ModTime().Unix(),
				Line:       line,
				Snippet:    snippet,
				MatchCount: matchCount,
			})
		}
		return nil
	})
	if err != nil {
		return searchResponse{}, err
	}

	slices.SortStableFunc(hits, func(a, b searchHit) int {
		if c := cmp.Compare(b.Modified, a.Modified); c != 0 {
			return c
		}
		return cmp.Compare(a.Path, b.Path)
	})

	truncated := false
	if len(hits) > limit {
		hits = hits[:limit]
		truncated = true
	}
	return searchResponse{Results: hits, Truncated: truncated}, nil
}
