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
	_ "time/tzdata" // honour TZ in minimal containers
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
	if len(token) < 6 {
		log.Fatal("NOTES_TOKEN or NOTES_TOKEN_FILE must contain at least 6 characters")
	}
	root, err := os.OpenRoot(os.Getenv("NOTES_ROOT"))
	if err != nil {
		log.Fatal("NOTES_ROOT: ", err)
	}
	defer root.Close()
	inbox := os.Getenv("NOTES_INBOX")
	if inbox == "" {
		inbox = defaultInbox
	}
	if !validInbox(inbox) {
		log.Fatal("NOTES_INBOX must be a relative .md path, e.g. ", defaultInbox)
	}
	newDir := strings.Trim(os.Getenv("NOTES_NEW_DIR"), "/")
	if !validNewDir(newDir) {
		log.Fatal("NOTES_NEW_DIR must be empty or a relative directory, e.g. notes")
	}
	attachments := os.Getenv("NOTES_ATTACHMENTS")
	if attachments == "" {
		attachments = defaultAttachments
	}
	if !validAttachments(attachments) {
		log.Fatal("NOTES_ATTACHMENTS must be a relative directory, e.g. ", defaultAttachments)
	}
	publicImages := os.Getenv("NOTES_PUBLIC_IMAGES") == "true"
	bind := os.Getenv("NOTES_BIND")
	if bind == "" {
		bind = "127.0.0.1:8789"
	}
	srv := &http.Server{Addr: bind, Handler: handler(&store{root: root, inbox: inbox, newDir: newDir, attachments: attachments, publicImages: publicImages}, token), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("notes API serving %s on %s", root.Name(), bind)
	if publicImages {
		log.Print("NOTES_PUBLIC_IMAGES: GET /v1/image does not require the token")
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
