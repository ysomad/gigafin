// Package logx adds request-scoped attributes, such as the request and user IDs, to every
// log record made with the request's context.
package logx

import (
	"context"
	"log/slog"
	"sync"
)

// RequestIDKey is the attribute key of the request ID.
const RequestIDKey = "request_id"

type ctxKey struct{}

// attrs is mutable, so attributes added deep in a handler, such as the user ID, reach
// records made by the middleware after the handler returns.
type attrs struct {
	mu        sync.Mutex
	requestID string
	list      []slog.Attr
}

// Start returns ctx with an empty attribute list of the request with id.
func Start(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, &attrs{
		requestID: requestID,
		list:      []slog.Attr{slog.String(RequestIDKey, requestID)},
	})
}

// Add appends attributes to the request of ctx. It does nothing outside a request.
func Add(ctx context.Context, a ...slog.Attr) {
	r, ok := ctx.Value(ctxKey{}).(*attrs)
	if !ok {
		return
	}
	r.mu.Lock()
	r.list = append(r.list, a...)
	r.mu.Unlock()
}

// RequestID returns the ID of the request of ctx, or "" outside a request.
func RequestID(ctx context.Context) string {
	if r, ok := ctx.Value(ctxKey{}).(*attrs); ok {
		return r.requestID
	}
	return ""
}

// Handler adds the attributes of the record's request to every record.
type Handler struct {
	slog.Handler
}

var _ slog.Handler = Handler{}

// Handle implements slog.Handler. It skips request attributes whose key the record
// already has, such as a user_id logged by a service, so no key appears twice.
func (h Handler) Handle(ctx context.Context, rec slog.Record) error {
	r, ok := ctx.Value(ctxKey{}).(*attrs)
	if !ok {
		return h.Handler.Handle(ctx, rec)
	}
	has := make(map[string]bool, rec.NumAttrs())
	rec.Attrs(func(a slog.Attr) bool {
		has[a.Key] = true
		return true
	})
	r.mu.Lock()
	for _, a := range r.list {
		if !has[a.Key] {
			rec.AddAttrs(a)
		}
	}
	r.mu.Unlock()
	return h.Handler.Handle(ctx, rec)
}

// WithAttrs implements slog.Handler.
func (h Handler) WithAttrs(a []slog.Attr) slog.Handler {
	return Handler{h.Handler.WithAttrs(a)}
}

// WithGroup implements slog.Handler.
func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{h.Handler.WithGroup(name)}
}
