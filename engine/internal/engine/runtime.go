package engine

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

// Runtime embeds the same protocol used by the CLI without a process or socket.
// Network/mutating requests are serialized; cache reads and events may run concurrently. Event handlers should
// enqueue work rather than synchronously calling Request.
type Runtime struct {
	mu      sync.Mutex
	stateMu sync.RWMutex
	once    sync.Once
	e       *Engine
	s       *Store
	ctx     context.Context
	cancel  context.CancelFunc
}

func OpenRuntime(dir string, emit func(string)) (*Runtime, error) {
	s, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{s: s, ctx: ctx, cancel: cancel, e: newEngine(ctx, s, &Wire{emit: emit})}, nil
}

func (r *Runtime) Request(input string) string {
	var req Request
	if len(input) > 1024*1024 || json.Unmarshal([]byte(input), &req) != nil || req.ID == "" {
		return response(req.ID, nil, rpcError("INVALID_REQUEST", "Expected an object with string id, method, and params (maximum 1 MiB)."))
	}
	// Cache/UI reads must remain responsive while connect is waiting on a socket.
	// These handlers only access Store's locks and atomic connection indicators.
	if req.Method == "hello" || req.Method == "status" || req.Method == "conversations.list" || req.Method == "messages.list" {
		if r.ctx.Err() != nil {
			return response(req.ID, nil, rpcError("CLIENT_CLOSED", "This engine is closed."))
		}
		r.stateMu.RLock()
		current := r.e
		r.stateMu.RUnlock()
		result, err := current.handle(req)
		return response(req.ID, result, err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.e.ctx.Err() != nil {
		return response(req.ID, nil, rpcError("CLIENT_CLOSED", "This engine is closed."))
	}
	// Reauthentication replaces the connection, not its durable encrypted identity.
	// Validate before stopping a working engine, and persist the fresh cookies again
	// after the old connection saves its final cookie updates during shutdown.
	if req.Method == "session.import" && r.e.client != nil {
		var p struct {
			Cookies map[string]string `json:"cookies"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			return response(req.ID, nil, rpcError("INVALID_PARAMS", "Invalid cookie parameters."))
		}
		if err := r.s.Import(p.Cookies); err != nil {
			return response(req.ID, nil, err)
		}
		wire := r.e.wire
		r.e.close()
		r.stateMu.Lock()
		r.e = newEngine(r.ctx, r.s, wire)
		r.stateMu.Unlock()
	}
	result, err := r.e.handle(req)
	return response(req.ID, result, err)
}

func response(id string, result any, err error) string {
	value := map[string]any{"id": id, "result": result}
	if err != nil {
		var rpc *RPCError
		if !errors.As(err, &rpc) {
			rpc = rpcError("ENGINE_ERROR", "The operation failed. Local profile data may be unavailable.")
		}
		value = map[string]any{"id": id, "error": rpc}
	}
	data, _ := json.Marshal(value)
	return string(data)
}

func (r *Runtime) Close() {
	r.once.Do(func() {
		r.cancel() // Interrupt an in-flight connect before waiting for its request lock.
		r.mu.Lock()
		defer r.mu.Unlock()
		r.e.close()
		_ = r.s.Close()
	})
}
