package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const reservedMarkerPrefix = "<!-- floral-capture-"

var (
	dateHeadingPattern = regexp.MustCompile(`^## (\d{4}-\d{2}-\d{2})$`)
	dateMarkerPattern  = regexp.MustCompile(`^<!-- floral-capture-date:v1 (\d{4}-\d{2}-\d{2}) -->$`)
	entryMarkerPattern = regexp.MustCompile(`^<!-- floral-capture-entry:v1 id=([A-Za-z0-9._:-]+) sha256=([a-f0-9]{64}) at=([^ ]+) -->$`)
	idPattern          = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key was already used with different content")
	ErrReservedMarker      = errors.New("content contains a reserved capture marker")
)

type Clock func() time.Time

type MarkdownStore struct {
	filePath    string
	lockPath    string
	location    *time.Location
	lockTimeout time.Duration
	now         Clock
	mu          sync.Mutex
}

type AppendResult struct {
	ID        string
	CreatedAt time.Time
	Duplicate bool
}

type fileState struct {
	lastDate      string
	hasDateMarker bool
	entryHash     string
	entryTime     time.Time
	newline       string
	tail          string
}

func NewMarkdownStore(filePath string, location *time.Location, lockTimeout time.Duration) *MarkdownStore {
	return &MarkdownStore{
		filePath:    filePath,
		lockPath:    filePath + ".lock",
		location:    location,
		lockTimeout: lockTimeout,
		now:         time.Now,
	}
}

func (s *MarkdownStore) Append(id, content string) (AppendResult, error) {
	if !idPattern.MatchString(id) {
		return AppendResult{}, errors.New("invalid idempotency key")
	}
	if strings.Contains(content, reservedMarkerPrefix) {
		return AppendResult{}, ErrReservedMarker
	}

	hashBytes := sha256.Sum256([]byte(content))
	contentHash := hex.EncodeToString(hashBytes[:])

	s.mu.Lock()
	defer s.mu.Unlock()

	release, err := acquireFileLock(s.lockPath, s.lockTimeout)
	if err != nil {
		return AppendResult{}, err
	}
	defer func() { _ = release() }()

	if err := validateTargetPath(s.filePath); err != nil {
		return AppendResult{}, err
	}

	state, err := inspectFile(s.filePath, id)
	if err != nil {
		return AppendResult{}, err
	}
	if state.entryHash != "" {
		if state.entryHash != contentHash {
			return AppendResult{}, ErrIdempotencyConflict
		}
		return AppendResult{ID: id, CreatedAt: state.entryTime, Duplicate: true}, nil
	}

	createdAt := s.now().In(s.location)
	block := buildAppendBlock(state, id, contentHash, content, createdAt)

	file, err := os.OpenFile(s.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return AppendResult{}, err
	}
	if _, err = io.WriteString(file, block); err != nil {
		_ = file.Close()
		return AppendResult{}, err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return AppendResult{}, err
	}
	if err = file.Close(); err != nil {
		return AppendResult{}, err
	}

	return AppendResult{ID: id, CreatedAt: createdAt, Duplicate: false}, nil
}

func inspectFile(path, requestedID string) (fileState, error) {
	state := fileState{newline: "\n"}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	var markerDate string
	var fallbackDate string
	var inFence bool
	var fenceByte byte
	var fenceLength int
	for {
		line, readErr := reader.ReadString('\n')
		if strings.HasSuffix(line, "\r\n") && state.newline == "\n" {
			state.newline = "\r\n"
		}
		state.tail = keepTail(state.tail, line, 4)
		trimmed := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")

		if match := dateMarkerPattern.FindStringSubmatch(trimmed); match != nil {
			markerDate = match[1]
			state.hasDateMarker = true
		}
		if match := entryMarkerPattern.FindStringSubmatch(trimmed); match != nil && match[1] == requestedID {
			state.entryHash = match[2]
			state.entryTime, _ = time.Parse(time.RFC3339, match[3])
		}

		if markerByte, markerLength, isFence := fenceMarker(trimmed); isFence {
			if !inFence {
				inFence, fenceByte, fenceLength = true, markerByte, markerLength
			} else if markerByte == fenceByte && markerLength >= fenceLength {
				inFence = false
			}
		} else if !inFence {
			if match := dateHeadingPattern.FindStringSubmatch(trimmed); match != nil {
				fallbackDate = match[1]
			}
		}

		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fileState{}, readErr
		}
	}

	if markerDate != "" {
		state.lastDate = markerDate
	} else {
		state.lastDate = fallbackDate
	}
	return state, nil
}

func fenceMarker(line string) (byte, int, bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || len(trimmed) < 3 {
		return 0, 0, false
	}
	marker := trimmed[0]
	if marker != '`' && marker != '~' {
		return 0, 0, false
	}
	length := 0
	for length < len(trimmed) && trimmed[length] == marker {
		length++
	}
	return marker, length, length >= 3
}

func buildAppendBlock(
	state fileState,
	id string,
	contentHash string,
	content string,
	createdAt time.Time,
) string {
	newline := state.newline
	var block strings.Builder
	block.WriteString(separatorForTail(state.tail, newline))

	date := createdAt.Format("2006-01-02")
	if state.lastDate != date {
		fmt.Fprintf(&block, "## %s%s%s", date, newline, newline)
		fmt.Fprintf(&block, "<!-- floral-capture-date:v1 %s -->%s%s", date, newline, newline)
	} else if !state.hasDateMarker {
		fmt.Fprintf(&block, "<!-- floral-capture-date:v1 %s -->%s%s", date, newline, newline)
	}

	fmt.Fprintf(&block, "### %s%s%s", createdAt.Format("15:04:05"), newline, newline)
	block.WriteString(content)
	block.WriteString(contentTerminator(content, newline))
	fmt.Fprintf(
		&block,
		"<!-- floral-capture-entry:v1 id=%s sha256=%s at=%s -->%s%s",
		id,
		contentHash,
		createdAt.Format(time.RFC3339),
		newline,
		newline,
	)
	return block.String()
}

func separatorForTail(tail, newline string) string {
	if tail == "" || strings.HasSuffix(tail, newline+newline) {
		return ""
	}
	if strings.HasSuffix(tail, newline) {
		return newline
	}
	return newline + newline
}

func contentTerminator(content, newline string) string {
	if strings.HasSuffix(content, newline+newline) {
		return ""
	}
	if strings.HasSuffix(content, newline) {
		return newline
	}
	return newline + newline
}

func keepTail(previous, next string, size int) string {
	combined := previous + next
	if len(combined) <= size {
		return combined
	}
	return combined[len(combined)-size:]
}
