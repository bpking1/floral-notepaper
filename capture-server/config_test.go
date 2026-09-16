package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigFromEnv(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("CAPTURE_FILE", filepath.Join(directory, "Inbox.md"))
	t.Setenv("CAPTURE_TIMEZONE", "Asia/Shanghai")
	t.Setenv("CAPTURE_TOKEN", testToken)
	t.Setenv("CAPTURE_BIND", "0.0.0.0:9999")
	t.Setenv("CAPTURE_MAX_BODY_BYTES", "4096")
	t.Setenv("CAPTURE_LOCK_TIMEOUT", "750ms")

	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.BindAddress != "0.0.0.0:9999" || config.MaxBodySize != 4096 {
		t.Fatalf("unexpected config: %+v", config)
	}
	if config.Location.String() != "Asia/Shanghai" {
		t.Fatalf("location = %s", config.Location)
	}
}

func TestLoadConfigReadsTokenFile(t *testing.T) {
	directory := t.TempDir()
	tokenPath := filepath.Join(directory, "token")
	if err := os.WriteFile(tokenPath, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE_FILE", filepath.Join(directory, "Inbox.md"))
	t.Setenv("CAPTURE_TIMEZONE", "UTC")
	t.Setenv("CAPTURE_TOKEN", "")
	t.Setenv("CAPTURE_TOKEN_FILE", tokenPath)

	config, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if config.Token != testToken {
		t.Fatalf("token was not trimmed/read correctly: %q", config.Token)
	}
}

func TestLoadConfigRejectsUnsafeOrIncompleteValues(t *testing.T) {
	directory := t.TempDir()
	absolutePath := filepath.Join(directory, "Inbox.md")

	tests := []struct {
		name     string
		filePath string
		timezone string
		token    string
		want     string
	}{
		{name: "relative file", filePath: "Inbox.md", timezone: "UTC", token: testToken, want: "absolute"},
		{name: "wrong extension", filePath: filepath.Join(directory, "Inbox.txt"), timezone: "UTC", token: testToken, want: ".md"},
		{name: "missing timezone", filePath: absolutePath, timezone: "", token: testToken, want: "TIMEZONE"},
		{name: "short token", filePath: absolutePath, timezone: "UTC", token: "short", want: "32"},
		{name: "invalid timezone", filePath: absolutePath, timezone: "Moon/Base", token: testToken, want: "invalid CAPTURE_TIMEZONE"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CAPTURE_FILE", test.filePath)
			t.Setenv("CAPTURE_TIMEZONE", test.timezone)
			t.Setenv("CAPTURE_TOKEN", test.token)
			t.Setenv("CAPTURE_TOKEN_FILE", "")
			_, err := LoadConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadConfigRejectsSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	realPath := filepath.Join(directory, "real.md")
	linkPath := filepath.Join(directory, "Inbox.md")
	if err := os.WriteFile(realPath, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("CAPTURE_FILE", linkPath)
	t.Setenv("CAPTURE_TIMEZONE", "UTC")
	t.Setenv("CAPTURE_TOKEN", testToken)
	t.Setenv("CAPTURE_TOKEN_FILE", "")
	_, err := LoadConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error = %v, want symbolic link rejection", err)
	}
}
