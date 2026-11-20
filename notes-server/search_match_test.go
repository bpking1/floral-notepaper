package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitSearchTermsAndMatchDocument(t *testing.T) {
	terms := splitSearchTerms("  工作   方案 ")
	if len(terms) != 2 || terms[0] != "工作" || terms[1] != "方案" {
		t.Fatalf("unexpected terms: %#v", terms)
	}

	content := "# 会议纪要\n第一段闲聊\n这里讨论工作计划与年度方案，包含多次方案对比。\n第四行补充"
	matched, line, snippet, count := matchDocument("工作/2026-周报.md", content, terms)
	if !matched || line != 3 || count != 3 {
		t.Fatalf("got matched=%v line=%d count=%d, want true/3/3", matched, line, count)
	}
	if !strings.Contains(snippet, "方案") {
		t.Fatalf("snippet missing match: %q", snippet)
	}

	// 词条可分别命中路径与正文（AND 语义）
	matchedPathBody, line2, _, count2 := matchDocument("日记/旅行.md", "今天喝了手冲咖啡\n天气不错", splitSearchTerms("旅行 咖啡"))
	if !matchedPathBody || line2 != 1 || count2 != 1 {
		t.Fatalf("path+body AND failed: matched=%v line=%d count=%d", matchedPathBody, line2, count2)
	}

	// 仅路径命中
	matchedPathOnly, line3, snip3, count3 := matchDocument("工作/架构设计.md", "\n正文第一行摘要", splitSearchTerms("架构"))
	if !matchedPathOnly || line3 != 0 || count3 != 0 || snip3 != "正文第一行摘要" {
		t.Fatalf("path-only failed: matched=%v line=%d count=%d snip=%q", matchedPathOnly, line3, count3, snip3)
	}

	// UTF-8 中文超长行居中截取不应切断字符
	longPrefix := strings.Repeat("记录", 70)
	longLine := longPrefix + "核心关键词在这里" + strings.Repeat("后续", 60)
	matchedLong, _, snipLong, _ := matchDocument("test.md", longLine, splitSearchTerms("核心关键词"))
	if !matchedLong || !utf8.ValidString(snipLong) || !strings.Contains(snipLong, "核心关键词") {
		t.Fatalf("long UTF-8 snippet invalid: matched=%v snip=%q", matchedLong, snipLong)
	}
	if !strings.HasPrefix(snipLong, "…") || !strings.HasSuffix(snipLong, "…") {
		t.Fatalf("expected ellipsis on clipped snippet: %q", snipLong)
	}
}
