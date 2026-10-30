package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ondics/yaml2video/internal/server/controller"
)

// Serve runs the HTTP API and cache cleanup until the process is interrupted.
func Serve() error {
	backend, err := controller.New(controller.Config{
		DataDir: os.Getenv("YAML2VIDEO_DATA_DIR"),
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cleanupDone := make(chan struct{})
	go func() {
		defer close(cleanupDone)
		backend.RunCleanup(ctx)
	}()

	server := &http.Server{
		Addr:              ":8080",
		Handler:           NewRouter(backend),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
		// WriteTimeout is deliberately unset for long-running SSE renders.
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		timeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(timeout); err != nil {
			log.Printf("HTTP shutdown: %v", err)
			_ = server.Close()
		}
	}()

	log.Printf("HTTP server listening on %s", server.Addr)
	err = server.ListenAndServe()
	stop()
	<-shutdownDone
	<-cleanupDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
