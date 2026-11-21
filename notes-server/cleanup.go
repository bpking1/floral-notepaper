package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultCleanupInterval = 24 * time.Hour
	defaultCleanupMinAge   = 7 * 24 * time.Hour
	cleanupReuseTouchAge   = time.Hour
	cleanupWriteTimeout    = 2 * time.Minute
	cleanupReadChunkSize   = 64 * 1024
	maxCleanupVisited      = 500000
)

var (
	errCleanupParam  = errors.New("invalid cleanup query parameter")
	errAllReferenced = errors.New("all candidate images referenced")
)

type cleanupOptions struct {
	minAge time.Duration
	dryRun bool
}

type orphanImage struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Modified  int64  `json:"modified"` // Unix seconds
	TrashedTo string `json:"trashedTo,omitempty"`
}

type cleanupResult struct {
	DryRun        bool          `json:"dryRun"`
	MinAge        string        `json:"minAge"`
	ScannedNotes  int           `json:"scannedNotes"`
	ScannedImages int           `json:"scannedImages"`
	TotalBytes    int64         `json:"totalBytes"`
	Images        []orphanImage `json:"images"`
}

type orphanCandidate struct {
	name    string
	path    string
	size    int64
	modTime time.Time
}

func (s *store) defaultCleanupMinAge() time.Duration {
	if s.cleanupMinAge > 0 {
		return s.cleanupMinAge
	}
	return defaultCleanupMinAge
}

// recordImageReuse keeps deduplicated uploads from falling below the orphan
// grace period when an older image is pasted into a new draft. Immediate
// retries keep the original mtime untouched; older files refresh their disk
// mtime so the grace period also survives server restarts.
func (s *store) recordImageReuse(images *os.Root, name string) {
	now := s.clock()
	minAge := s.defaultCleanupMinAge()

	s.reuseMu.Lock()
	if s.recentReused == nil {
		s.recentReused = make(map[string]time.Time)
	}
	if len(s.recentReused) > 4096 {
		cutoff := now.Add(-minAge)
		for k, ts := range s.recentReused {
			if ts.Before(cutoff) {
				delete(s.recentReused, k)
			}
		}
	}
	s.recentReused[name] = now
	s.reuseMu.Unlock()

	touchAge := cleanupReuseTouchAge
	if minAge/2 < touchAge {
		touchAge = minAge / 2
	}
	info, err := images.Lstat(name)
	if err == nil && info.Mode().IsRegular() && now.Sub(info.ModTime()) >= touchAge {
		_ = images.Chtimes(name, now, now)
	}
}

func (s *store) effectiveImageModTime(name string, diskMod time.Time) time.Time {
	s.reuseMu.Lock()
	reusedAt, ok := s.recentReused[name]
	s.reuseMu.Unlock()
	if ok && reusedAt.After(diskMod) {
		return reusedAt
	}
	return diskMod
}

func isManagedImageName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		return true
	default:
		return false
	}
}

// parseCleanupDuration parses Go durations (e.g. "168h", "0") and integer day
// suffixes (e.g. "7d", "1d").
func parseCleanupDuration(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, fmt.Errorf("%w: duration must not be empty", errCleanupParam)
	}
	if trimmed == "0" {
		return 0, nil
	}
	if strings.HasSuffix(trimmed, "d") || strings.HasSuffix(trimmed, "D") {
		numStr := trimmed[:len(trimmed)-1]
		days, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: invalid duration %q", errCleanupParam, raw)
		}
		const maxDays = math.MaxInt64 / int64(24*time.Hour)
		const minDays = math.MinInt64 / int64(24*time.Hour)
		if days > maxDays || days < minDays {
			return 0, fmt.Errorf("%w: duration %q out of range", errCleanupParam, raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid duration %q", errCleanupParam, raw)
	}
	return d, nil
}

func parseCleanupInterval(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultCleanupInterval, nil
	}
	switch strings.ToLower(trimmed) {
	case "0", "off", "false", "disabled":
		return 0, nil
	}
	d, err := parseCleanupDuration(trimmed)
	if err != nil {
		return 0, err
	}
	if d < 0 {
		return 0, fmt.Errorf("%w: cleanup interval must be non-negative", errCleanupParam)
	}
	return d, nil
}

func parseCleanupMinAgeEnv(raw string) (time.Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultCleanupMinAge, nil
	}
	d, err := parseCleanupDuration(trimmed)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("%w: cleanup minAge must be positive", errCleanupParam)
	}
	return d, nil
}

func parseCleanupBool(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "t", "yes":
		return true, nil
	case "false", "0", "f", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%w: dryRun must be true or false", errCleanupParam)
	}
}

func (s *store) cleanupOrphanImages(ctx context.Context, opts cleanupOptions) (cleanupResult, error) {
	if opts.minAge < 0 {
		return cleanupResult{}, fmt.Errorf("%w: minAge must be non-negative", errCleanupParam)
	}
	if err := ctx.Err(); err != nil {
		return cleanupResult{}, err
	}

	res := cleanupResult{
		DryRun: opts.dryRun,
		MinAge: opts.minAge.String(),
		Images: []orphanImage{},
	}

	dir := s.attachmentsDir()
	images, err := openImageDirectory(s.root, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return cleanupResult{}, err
	}
	entries, err := fs.ReadDir(images.FS(), ".")
	_ = images.Close()
	if err != nil {
		return cleanupResult{}, err
	}

	now := s.clock()
	candidates := make(map[string]orphanCandidate)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return cleanupResult{}, err
		}
		if !entry.Type().IsRegular() || !isManagedImageName(entry.Name()) {
			continue
		}
		res.ScannedImages++
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return cleanupResult{}, err
		}
		effectiveMod := s.effectiveImageModTime(entry.Name(), info.ModTime())
		if now.Sub(effectiveMod) < opts.minAge {
			continue
		}
		candidates[entry.Name()] = orphanCandidate{
			name:    entry.Name(),
			path:    path.Join(dir, entry.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		}
	}

	if len(candidates) == 0 {
		return res, nil
	}

	seqBefore := s.writeSeq.Load()
	scannedNotes, err := s.scanNotesForCandidates(ctx, candidates)
	if err != nil {
		return cleanupResult{}, err
	}
	res.ScannedNotes = scannedNotes
	if len(candidates) == 0 {
		return res, nil
	}

	if opts.dryRun {
		names := sortedCandidateNames(candidates)
		for _, name := range names {
			c := candidates[name]
			res.Images = append(res.Images, orphanImage{
				Path:     c.path,
				Size:     c.size,
				Modified: c.modTime.Unix(),
			})
			res.TotalBytes += c.size
		}
		return res, nil
	}

	// Acquire locks in canonical order: s.mu -> flock(s.root) -> flock(images).
	s.mu.Lock()
	defer s.mu.Unlock()
	unlockRoot, err := lockDirectory(ctx, s.root)
	if err != nil {
		return cleanupResult{}, err
	}
	defer unlockRoot()

	imagesLocked, err := openImageDirectory(s.root, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return res, nil
	}
	if err != nil {
		return cleanupResult{}, err
	}
	defer imagesLocked.Close()
	unlockImages, err := lockDirectory(ctx, imagesLocked)
	if err != nil {
		return cleanupResult{}, err
	}
	defer unlockImages()

	// If any API write or trash occurred during the unlocked scan, re-scan
	// under lock before moving anything.
	if s.writeSeq.Load() != seqBefore {
		scannedNotes, err = s.scanNotesForCandidates(ctx, candidates)
		if err != nil {
			return cleanupResult{}, err
		}
		res.ScannedNotes = scannedNotes
		if len(candidates) == 0 {
			return res, nil
		}
	}

	names := sortedCandidateNames(candidates)
	now = s.clock()
	var verified []orphanCandidate
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return cleanupResult{}, err
		}
		info, err := imagesLocked.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return cleanupResult{}, err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		effectiveMod := s.effectiveImageModTime(name, info.ModTime())
		if now.Sub(effectiveMod) < opts.minAge {
			continue
		}
		c := candidates[name]
		c.size = info.Size()
		c.modTime = info.ModTime()
		verified = append(verified, c)
	}

	if len(verified) == 0 {
		return res, nil
	}

	trashSubdir := path.Join(trashDir, dir)
	trashParent, err := ensureImageDirectory(s.root, trashSubdir)
	if err != nil {
		return cleanupResult{}, err
	}
	defer trashParent.Close()

	for _, c := range verified {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		destName, err := uniqueTrashName(trashParent, c.name, s.clock())
		if err != nil {
			return res, err
		}
		destPath := path.Join(trashSubdir, destName)
		if err := s.root.Rename(c.path, destPath); err != nil {
			return res, err
		}
		res.Images = append(res.Images, orphanImage{
			Path:      c.path,
			Size:      c.size,
			Modified:  c.modTime.Unix(),
			TrashedTo: destPath,
		})
		res.TotalBytes += c.size
	}

	if err := syncImageDirectory(imagesLocked); err != nil {
		return res, err
	}
	if err := syncImageDirectory(trashParent); err != nil {
		return res, err
	}
	return res, nil
}

func sortedCandidateNames(candidates map[string]orphanCandidate) []string {
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	slices.SortStableFunc(names, func(a, b string) int { return cmp.Compare(a, b) })
	return names
}

// scanNotesForCandidates walks all .md/.markdown files (including .trash/,
// excluding other hidden dirs like .git) and removes any candidate whose
// filename or URL-decoded filename appears in note text. Fails closed on any
// traversal or read error so incomplete scans never delete images.
func (s *store) scanNotesForCandidates(ctx context.Context, candidates map[string]orphanCandidate) (int, error) {
	if len(candidates) == 0 {
		return 0, nil
	}
	scannedNotes := 0
	visited := 0
	err := fs.WalkDir(s.root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		visited++
		if visited > maxCleanupVisited {
			return errors.New("directory too large for safe orphan image scan")
		}
		if name != "." && name != trashDir && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return fs.SkipDir
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(path.Ext(name))
		if ext != ".md" && ext != ".markdown" {
			return nil
		}
		if err := s.scanNoteFileForCandidates(ctx, name, candidates); err != nil {
			return err
		}
		scannedNotes++
		if len(candidates) == 0 {
			return errAllReferenced
		}
		return nil
	})
	if errors.Is(err, errAllReferenced) {
		return scannedNotes, nil
	}
	if err != nil {
		return 0, err
	}
	return scannedNotes, nil
}

func (s *store) scanNoteFileForCandidates(ctx context.Context, name string, candidates map[string]orphanCandidate) error {
	f, err := s.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return errPath
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errPath
	}

	maxName := 0
	for candName := range candidates {
		if len(candName) > maxName {
			maxName = len(candName)
		}
	}
	if maxName == 0 {
		return nil
	}

	overlap := maxName*3 + 2
	readBufSize := cleanupReadChunkSize
	if overlap*2 > readBufSize {
		readBufSize = overlap * 2
	}
	buf := make([]byte, overlap+readBufSize)
	carry := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := f.Read(buf[carry : carry+readBufSize])
		if n > 0 {
			window := buf[:carry+n]
			matchCandidatesInWindow(window, candidates)
			if len(candidates) == 0 {
				return nil
			}
			if len(window) > overlap {
				copy(buf[:overlap], window[len(window)-overlap:])
				carry = overlap
			} else {
				carry = len(window)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

func matchCandidatesInWindow(window []byte, candidates map[string]orphanCandidate) {
	for name := range candidates {
		if bytes.Contains(window, []byte(name)) {
			delete(candidates, name)
		}
	}
	if len(candidates) == 0 {
		return
	}
	if bytes.IndexByte(window, '%') >= 0 {
		decoded := lenientPercentDecode(window, false)
		for name := range candidates {
			if bytes.Contains(decoded, []byte(name)) {
				delete(candidates, name)
			}
		}
		if len(candidates) == 0 {
			return
		}
	}
	if bytes.IndexByte(window, '+') >= 0 {
		hasSpace := false
		for name := range candidates {
			if strings.ContainsRune(name, ' ') {
				hasSpace = true
				break
			}
		}
		if hasSpace {
			decodedPlus := lenientPercentDecode(window, true)
			for name := range candidates {
				if strings.ContainsRune(name, ' ') && bytes.Contains(decodedPlus, []byte(name)) {
					delete(candidates, name)
				}
			}
		}
	}
}

func fromHex(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

func lenientPercentDecode(src []byte, plusAsSpace bool) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		switch {
		case src[i] == '%' && i+2 < len(src):
			if hi, ok1 := fromHex(src[i+1]); ok1 {
				if lo, ok2 := fromHex(src[i+2]); ok2 {
					out = append(out, (hi<<4)|lo)
					i += 2
					continue
				}
			}
			out = append(out, '%')
		case plusAsSpace && src[i] == '+':
			out = append(out, ' ')
		default:
			out = append(out, src[i])
		}
	}
	return out
}

// ensureImageDirectory creates dir component by component while verifying no
// directory component is a symbolic link, returning an *os.Root anchored to it.
func ensureImageDirectory(root *os.Root, name string) (*os.Root, error) {
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
		if errors.Is(err, fs.ErrNotExist) {
			if mkErr := current.Mkdir(part, 0700); mkErr != nil && !errors.Is(mkErr, fs.ErrExist) {
				current.Close()
				return nil, mkErr
			}
			info, err = current.Lstat(part)
		}
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

func uniqueTrashName(trashParent *os.Root, name string, now time.Time) (string, error) {
	if _, err := trashParent.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return name, nil
	} else if err != nil {
		return "", err
	}
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	stamp := now.Format("20060102-150405")
	stamped := fmt.Sprintf("%s-%s%s", stem, stamp, ext)
	if _, err := trashParent.Lstat(stamped); errors.Is(err, fs.ErrNotExist) {
		return stamped, nil
	} else if err != nil {
		return "", err
	}
	for i := 2; i <= 1000; i++ {
		candidate := fmt.Sprintf("%s-%s-%d%s", stem, stamp, i, ext)
		if _, err := trashParent.Lstat(candidate); errors.Is(err, fs.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", errors.New("too many trashed copies with the same image name")
}

func startImageCleanupLoop(ctx context.Context, s *store, interval, minAge time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runCtx, cancel := context.WithTimeout(ctx, cleanupWriteTimeout)
				res, err := s.cleanupOrphanImages(runCtx, cleanupOptions{minAge: minAge, dryRun: false})
				cancel()
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						log.Printf("image cleanup failed: %v", err)
					}
					continue
				}
				if len(res.Images) > 0 {
					log.Printf("image cleanup moved %d unused image(s) (%d bytes) to %s/ (scanned %d notes, %d images)",
						len(res.Images), res.TotalBytes, trashDir, res.ScannedNotes, res.ScannedImages)
				}
			}
		}
	}()
	return done
}
