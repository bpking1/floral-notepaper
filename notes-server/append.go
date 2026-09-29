package main

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	defaultInbox   = "Chat.md"
	maxAppendIDs   = 4096
	maxAppendTries = 5
)

var (
	errAppendEmpty = errors.New("entry is empty")
	errAppendID    = errors.New("entry id must be 1-128 characters")
	errAppendPath  = errors.New(`path must be a relative .md file without hidden parts or \ : * ? " < > | characters`)
	dayHeading     = regexp.MustCompile(`(?m)^#### (.+?)[ \t]*$`)
)

type appendRequest struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// Path, when set, is the note file to create or extend instead of the inbox.
	Path string `json:"path"`
}

type appendResult struct {
	Path     string `json:"path"`
	Revision string `json:"revision"`
}

// validInbox reports whether a pattern expands to a Markdown path the store accepts.
func validInbox(pattern string) bool {
	sample := expandInbox(pattern, time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC))
	return fs.ValidPath(sample) && !strings.ContainsAny(sample, "\\\x00") &&
		(strings.EqualFold(path.Ext(sample), ".md") || strings.EqualFold(path.Ext(sample), ".markdown"))
}

func expandInbox(pattern string, now time.Time) string {
	return strings.NewReplacer(
		"{YYYY}", now.Format("2006"),
		"{MM}", now.Format("01"),
		"{DD}", now.Format("02"),
	).Replace(pattern)
}

func (s *store) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// inboxPath is the inbox file an append at now writes to.
func (s *store) inboxPath(now time.Time) string {
	pattern := s.inbox
	if pattern == "" {
		pattern = defaultInbox
	}
	return expandInbox(pattern, now)
}

// formatEntry appends one capture the way Files.md writes Chat.md:
//
//	#### 29 September, Tuesday
//	- [ ] `14:32` first line
//	further lines
//
// A day heading is added when the file's last one is another day. Entries in
// a day follow each other directly, except after a multi-line entry, where a
// blank line keeps the new item from joining the previous paragraph.
func formatEntry(existing string, now time.Time, title, text string) string {
	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	lastLine := func() string {
		trimmed := strings.TrimRight(b.String(), "\n")
		return trimmed[strings.LastIndex(trimmed, "\n")+1:]
	}
	blankLine := func() {
		if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
			b.WriteString("\n")
		}
	}
	day := now.Format("2 January, Monday")
	if matches := dayHeading.FindAllStringSubmatch(existing, -1); len(matches) == 0 || matches[len(matches)-1][1] != day {
		blankLine()
		b.WriteString("#### " + day + "\n")
	} else if last := lastLine(); !strings.HasPrefix(last, "- [") && !strings.HasPrefix(last, "#### ") {
		blankLine()
	}
	body := text
	if title != "" {
		body = strings.TrimRight(title+"\n"+text, "\n")
	}
	b.WriteString("- [ ] `" + now.Format("15:04") + "` " + body + "\n")
	return b.String()
}

// validNotePath reports whether name is a note the client may create: a
// relative Markdown path that other devices can sync (no characters Windows
// rejects) and that stays out of hidden directories such as .git.
func validNotePath(name string) bool {
	if !fs.ValidPath(name) || name == "." {
		return false
	}
	if ext := strings.ToLower(path.Ext(name)); ext != ".md" && ext != ".markdown" {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `\:*?"<>|`) ||
			strings.ContainsFunc(part, unicode.IsControl) {
			return false
		}
	}
	return true
}

// validNewDir reports whether dir can hold titled captures: empty (the notes
// root) or a directory that validNotePath would accept notes in.
func validNewDir(dir string) bool {
	return dir == "" || validNotePath(dir+"/x.md")
}

// appendNote adds text to a note file, separated from existing content by a blank line.
func appendNote(existing, text string) string {
	switch {
	case text == "":
		return existing
	case strings.TrimSpace(existing) == "":
		return text + "\n"
	case strings.HasSuffix(existing, "\n\n"):
		return existing + text + "\n"
	case strings.HasSuffix(existing, "\n"):
		return existing + "\n" + text + "\n"
	default:
		return existing + "\n\n" + text + "\n"
	}
}

func normalizeEntry(req appendRequest) (appendRequest, error) {
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" || len(req.ID) > 128 {
		return req, errAppendID
	}
	req.Title = strings.Join(strings.Fields(req.Title), " ")
	req.Text = strings.TrimRight(strings.ReplaceAll(req.Text, "\r\n", "\n"), " \t\n")
	req.Text = strings.TrimLeft(req.Text, "\n")
	if req.Title == "" && req.Text == "" {
		return req, errAppendEmpty
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path != "" && !validNotePath(req.Path) {
		return req, errAppendPath
	}
	return req, nil
}

// append adds one capture entry to the current inbox file, or with req.Path
// creates or extends that note. Retries with the same id return the first
// result without writing again; the id log lives in memory only, so it does
// not survive a restart.
func (s *store) append(ctx context.Context, req appendRequest) (appendResult, error) {
	req, err := normalizeEntry(req)
	if err != nil {
		return appendResult{}, err
	}
	s.appendMu.Lock()
	defer s.appendMu.Unlock()
	if done, ok := s.appended[req.ID]; ok {
		return done, nil
	}
	now := s.clock()
	name := s.inboxPath(now)
	format := func(existing string) string { return formatEntry(existing, now, req.Title, req.Text) }
	if req.Path != "" {
		name = req.Path
		format = func(existing string) string { return appendNote(existing, req.Text) }
	}
	if dir := path.Dir(name); dir != "." {
		if err := s.root.MkdirAll(dir, 0700); err != nil {
			return appendResult{}, err
		}
	}
	// Validate after creating the directory; it also rejects symlinked parents.
	if err := s.validate(name); err != nil {
		return appendResult{}, err
	}
	for try := 0; ; try++ {
		old, err := s.read(name)
		create := errors.Is(err, fs.ErrNotExist)
		if err != nil && !create {
			return appendResult{}, err
		}
		revision, err := s.write(ctx, name, []byte(format(old.Content)), old.Revision, create)
		// An external editor saved in between; re-read and append to its version.
		if errors.Is(err, errConflict) && try < maxAppendTries {
			continue
		}
		if err != nil {
			return appendResult{}, err
		}
		result := appendResult{Path: name, Revision: revision}
		s.remember(req.ID, result)
		return result, nil
	}
}

func (s *store) remember(id string, result appendResult) {
	if s.appended == nil {
		s.appended = map[string]appendResult{}
	}
	if len(s.appended) >= maxAppendIDs {
		oldest := s.appendOrder[0]
		s.appendOrder = s.appendOrder[1:]
		delete(s.appended, oldest)
	}
	s.appended[id] = result
	s.appendOrder = append(s.appendOrder, id)
}
