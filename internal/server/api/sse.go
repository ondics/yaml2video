package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// SSE serializes event and heartbeat writes. A failed write cancels its context
// so backend work using Context can stop.
type SSE struct {
	writer  http.ResponseWriter
	stream  *http.ResponseController
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	started bool
}

// ServeSSE calls handle with a streaming connection tied to the request context.
// Headers are committed on the first send, allowing ordinary HTTP errors before it.
func ServeSSE(w http.ResponseWriter, r *http.Request, handle func(*SSE)) {
	if !supportsFlush(w) {
		_ = JSON(w, http.StatusInternalServerError, Map{"error": "streaming is not supported"})
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	connection := &SSE{
		writer: w,
		stream: http.NewResponseController(w),
		ctx:    ctx,
		cancel: cancel,
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)

		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				connection.mu.Lock()
				// Do not start a stream while the callback is still validating the request.
				if connection.started {
					_ = connection.write(": heartbeat\n\n")
				}
				connection.mu.Unlock()
			}
		}
	}()

	defer func() {
		cancel()
		<-stopped
	}()

	handle(connection)
}

// Context is canceled when the request ends, a send fails, or ServeSSE returns.
func (s *SSE) Context() context.Context {
	return s.ctx
}

func (s *SSE) Send(event string, value any) error {
	if strings.ContainsAny(event, "\r\n\x00") {
		s.cancel()
		return errors.New("SSE event name must not contain line breaks or NUL")
	}

	data, err := json.Marshal(value)
	if err != nil {
		s.cancel()
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(fmt.Sprintf("event: %s\ndata: %s\n\n", event, data))
}

// write requires the connection lock to be held.
func (s *SSE) write(frame string) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if !s.started {
		s.writer.Header().Set("Content-Type", "text/event-stream")
		s.writer.Header().Set("Cache-Control", "no-cache")
		s.writer.Header().Set("X-Accel-Buffering", "no")
		s.started = true
	}
	if _, err := io.WriteString(s.writer, frame); err != nil {
		s.cancel()
		return err
	}
	if err := s.stream.Flush(); err != nil {
		s.cancel()
		return err
	}
	return nil
}

func supportsFlush(w http.ResponseWriter) bool {
	for {
		switch writer := w.(type) {
		case interface{ FlushError() error }:
			return true
		case http.Flusher:
			return true
		case interface{ Unwrap() http.ResponseWriter }:
			w = writer.Unwrap()
		default:
			return false
		}
	}
}
