package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	logger := log.New(os.Stdout, "capture-api ", log.LstdFlags|log.LUTC)
	config, err := LoadConfigFromEnv()
	if err != nil {
		logger.Fatalf("configuration error: %v", err)
	}

	store := NewMarkdownStore(config.FilePath, config.Location, config.LockTimeout)
	api := NewAPIServer(store, config.Token, config.MaxBodySize, logger)
	server := &http.Server{
		Addr:              config.BindAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-shutdownSignals
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Printf("graceful shutdown failed: %v", err)
		}
	}()

	logger.Printf(
		"listening on %s; target=%s; timezone=%s",
		config.BindAddress,
		filepath.Base(config.FilePath),
		config.Location.String(),
	)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("server stopped: %v", err)
	}
	logger.Print("server stopped")
}
