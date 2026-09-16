package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	token := strings.TrimSpace(os.Getenv("NOTES_TOKEN"))
	if file := os.Getenv("NOTES_TOKEN_FILE"); file != "" {
		if token != "" {
			log.Fatal("set only one of NOTES_TOKEN and NOTES_TOKEN_FILE")
		}
		data, err := os.ReadFile(file)
		if err != nil {
			log.Fatal(err)
		}
		token = strings.TrimSpace(string(data))
	}
	if len(token) < 32 {
		log.Fatal("NOTES_TOKEN or NOTES_TOKEN_FILE must contain at least 32 characters")
	}
	root, err := os.OpenRoot(os.Getenv("NOTES_ROOT"))
	if err != nil {
		log.Fatal("NOTES_ROOT: ", err)
	}
	defer root.Close()
	bind := os.Getenv("NOTES_BIND")
	if bind == "" {
		bind = "127.0.0.1:8788"
	}
	srv := &http.Server{Addr: bind, Handler: handler(&store{root: root}, token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("notes API listening on %s", bind)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
