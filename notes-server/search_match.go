package main

import (
	"strings"
	"unicode/utf8"
)

const maxSearchSnippetRunes = 120

type searchHit struct {
	Path       string `json:"path"`
	Modified   int64  `json:"modified"`
	Line       int    `json:"line"`
	Snippet    string `json:"snippet"`
	MatchCount int    `json:"matchCount"`
}

type searchResponse struct {
	Results   []searchHit `json:"results"`
	Truncated bool        `json:"truncated"`
}

func splitSearchTerms(raw string) []string {
	return strings.Fields(strings.ToLower(strings.TrimSpace(raw)))
}

func countOccurrences(haystack, needle string) (count int, firstByte int) {
	firstByte = -1
	if needle == "" {
		return 0, -1
	}
	offset := 0
	for {
		idx := strings.Index(haystack[offset:], needle)
		if idx < 0 {
			break
		}
		if firstByte < 0 {
			firstByte = offset + idx
		}
		count++
		offset += idx + len(needle)
	}
	return count, firstByte
}

func buildSearchSnippet(rawLine string, matchRuneIdx int, maxRunes int) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(rawLine, "\t", " "))
	if cleaned == "" {
		return ""
	}
	runes := []rune(cleaned)
	if len(runes) <= maxRunes || maxRunes <= 1 {
		return cleaned
	}
	if matchRuneIdx < 0 {
		matchRuneIdx = 0
	}
	if matchRuneIdx >= len(runes) {
		matchRuneIdx = len(runes) - 1
	}

	start := matchRuneIdx - maxRunes/3
	if start < 0 {
		start = 0
	}
	end := start + maxRunes
	if end > len(runes) {
		end = len(runes)
		start = max(0, end-maxRunes)
	}

	window := strings.TrimSpace(string(runes[start:end]))
	if start > 0 {
		window = "…" + window
	}
	if end < len(runes) {
		window = window + "…"
	}
	return window
}

func matchDocument(docPath, content string, terms []string) (matched bool, lineNum int, snippet string, matchCount int) {
	if len(terms) == 0 {
		return false, 0, "", 0
	}
	lowerPath := strings.ToLower(docPath)
	lowerContent := strings.ToLower(content)
	for _, term := range terms {
		if !strings.Contains(lowerPath, term) && !strings.Contains(lowerContent, term) {
			return false, 0, "", 0
		}
	}

	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")

	bestLine := 0
	bestDistinct := 0
	bestLineCount := 0
	bestRaw := ""
	bestMatchRune := 0
	totalMatches := 0
	firstNonEmpty := ""

	for i, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" {
			continue
		}
		if firstNonEmpty == "" {
			firstNonEmpty = trimmed
		}

		lowerLine := strings.ToLower(trimmed)
		lineMatches := 0
		distinctTerms := 0
		earliestByte := -1

		for _, term := range terms {
			cnt, firstByte := countOccurrences(lowerLine, term)
			if cnt > 0 {
				distinctTerms++
				lineMatches += cnt
				if earliestByte < 0 || firstByte < earliestByte {
					earliestByte = firstByte
				}
			}
		}

		totalMatches += lineMatches
		if distinctTerms > 0 && (distinctTerms > bestDistinct || (distinctTerms == bestDistinct && lineMatches > bestLineCount)) {
			bestLine = i + 1
			bestDistinct = distinctTerms
			bestLineCount = lineMatches
			bestRaw = trimmed
			bestMatchRune = utf8.RuneCountInString(lowerLine[:earliestByte])
		}
	}

	if bestLine > 0 {
		return true, bestLine, buildSearchSnippet(bestRaw, bestMatchRune, maxSearchSnippetRunes), totalMatches
	}
	if firstNonEmpty != "" {
		return true, 0, buildSearchSnippet(firstNonEmpty, 0, maxSearchSnippetRunes), 0
	}
	return true, 0, "", 0
}
