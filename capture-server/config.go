package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

const (
	defaultBindAddress = "127.0.0.1:8787"
	defaultMaxBodySize = int64(256 * 1024)
	defaultLockTimeout = 3 * time.Second
)

type Config struct {
	BindAddress string
	FilePath    string
	Location    *time.Location
	Token       string
	MaxBodySize int64
	LockTimeout time.Duration
}

func LoadConfigFromEnv() (Config, error) {
	filePath := strings.TrimSpace(os.Getenv("CAPTURE_FILE"))
	if filePath == "" {
		return Config{}, errors.New("CAPTURE_FILE is required")
	}
	if !filepath.IsAbs(filePath) {
		return Config{}, errors.New("CAPTURE_FILE must be an absolute path")
	}
	extension := strings.ToLower(filepath.Ext(filePath))
	if extension != ".md" && extension != ".markdown" {
		return Config{}, errors.New("CAPTURE_FILE must end with .md or .markdown")
	}
	if err := validateTargetPath(filePath); err != nil {
		return Config{}, err
	}

	timezone := strings.TrimSpace(os.Getenv("CAPTURE_TIMEZONE"))
	if timezone == "" {
		return Config{}, errors.New("CAPTURE_TIMEZONE is required, for example Asia/Shanghai or UTC")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return Config{}, fmt.Errorf("invalid CAPTURE_TIMEZONE: %w", err)
	}

	token, err := loadToken()
	if err != nil {
		return Config{}, err
	}

	maxBodySize, err := envInt64("CAPTURE_MAX_BODY_BYTES", defaultMaxBodySize)
	if err != nil {
		return Config{}, err
	}
	if maxBodySize < 1 || maxBodySize > 10*1024*1024 {
		return Config{}, errors.New("CAPTURE_MAX_BODY_BYTES must be between 1 and 10485760")
	}

	lockTimeout := defaultLockTimeout
	if raw := strings.TrimSpace(os.Getenv("CAPTURE_LOCK_TIMEOUT")); raw != "" {
		lockTimeout, err = time.ParseDuration(raw)
		if err != nil || lockTimeout <= 0 {
			return Config{}, errors.New("CAPTURE_LOCK_TIMEOUT must be a positive duration such as 3s")
		}
	}

	bindAddress := strings.TrimSpace(os.Getenv("CAPTURE_BIND"))
	if bindAddress == "" {
		bindAddress = defaultBindAddress
	}

	return Config{
		BindAddress: bindAddress,
		FilePath:    filepath.Clean(filePath),
		Location:    location,
		Token:       token,
		MaxBodySize: maxBodySize,
		LockTimeout: lockTimeout,
	}, nil
}

func validateTargetPath(filePath string) error {
	parent := filepath.Dir(filePath)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("CAPTURE_FILE parent directory is not accessible: %w", err)
	}
	if !parentInfo.IsDir() {
		return errors.New("CAPTURE_FILE parent is not a directory")
	}

	info, err := os.Lstat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("CAPTURE_FILE is not accessible: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("CAPTURE_FILE must not be a symbolic link")
	}
	if !info.Mode().IsRegular() {
		return errors.New("CAPTURE_FILE must be a regular file")
	}
	return nil
}

func loadToken() (string, error) {
	direct := strings.TrimSpace(os.Getenv("CAPTURE_TOKEN"))
	tokenFile := strings.TrimSpace(os.Getenv("CAPTURE_TOKEN_FILE"))
	if direct != "" && tokenFile != "" {
		return "", errors.New("set only one of CAPTURE_TOKEN or CAPTURE_TOKEN_FILE")
	}

	token := direct
	if tokenFile != "" {
		data, err := os.ReadFile(tokenFile)
		if err != nil {
			return "", fmt.Errorf("read CAPTURE_TOKEN_FILE: %w", err)
		}
		token = strings.TrimSpace(string(data))
	}

	if len(token) < 32 {
		return "", errors.New("capture token is required and must contain at least 32 characters")
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", errors.New("capture token must be a single line")
	}
	return token, nil
}

func envInt64(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return value, nil
}
