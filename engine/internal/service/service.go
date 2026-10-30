// Package service hosts one persistent Messenger profile over authenticated HTTP.
package service

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Caller interface{ Request(string) string }
type Handler struct {
	caller      Caller
	token       [32]byte
	mu          sync.Mutex
	subscribers map[chan string]struct{}
}

func New(token string) *Handler {
	return &Handler{token: sha256.Sum256([]byte(token)), subscribers: map[chan string]struct{}{}}
}

// Attach is called before the HTTP server starts.
func (h *Handler) Attach(caller Caller) { h.caller = caller }

// Publish never stalls the messaging engine behind a disconnected/slow client.
func (h *Handler) Publish(event string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			delete(h.subscribers, ch)
			close(ch)
		}
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// No cookie auth, query-string tokens, or CORS: browsers cannot silently use the service.
	provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	actual := sha256.Sum256([]byte(provided))
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(h.token[:], actual[:]) != 1 {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Header.Get("Origin") != "" {
		http.Error(w, "Browser origins are not supported", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/v1/rpc" && r.Method == http.MethodPost:
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "Expected application/json", 415)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Request too large", 413)
			return
		}
		if !json.Valid(body) {
			http.Error(w, "Invalid JSON", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, h.caller.Request(string(body)))
	case r.URL.Path == "/v1/events" && r.Method == http.MethodGet:
		h.events(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unavailable", 500)
		return
	}
	ch := make(chan string, 64)
	h.mu.Lock()
	if len(h.subscribers) >= 8 {
		h.mu.Unlock()
		http.Error(w, "Too many event clients", 429)
		return
	}
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.subscribers, ch); h.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	// Reconnecting clients refresh the authoritative cache; events are not a durable log.
	_, _ = fmt.Fprint(w, "data: {\"event\":\"resync\",\"data\":{}}\n\n")
	flusher.Flush()
	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
