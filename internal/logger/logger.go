package logger

import (
	"context"
	"log/slog"
	"sync"
)

const RequestIDKey = "request_id"

type ctxKey struct{}

type attrs struct {
	mu        sync.Mutex
	requestID string
	list      []slog.Attr
}

func Context(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, &attrs{
		requestID: requestID,
		list:      []slog.Attr{slog.String(RequestIDKey, requestID)},
	})
}

func WithAttrs(ctx context.Context, a ...slog.Attr) {
	r, ok := ctx.Value(ctxKey{}).(*attrs)
	if !ok {
		return
	}
	r.mu.Lock()
	r.list = append(r.list, a...)
	r.mu.Unlock()
}

func RequestID(ctx context.Context) string {
	if r, ok := ctx.Value(ctxKey{}).(*attrs); ok {
		return r.requestID
	}
	return ""
}

type Handler struct {
	slog.Handler
}

var _ slog.Handler = Handler{}

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

func (h Handler) WithAttrs(a []slog.Attr) slog.Handler {
	return Handler{h.Handler.WithAttrs(a)}
}

func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{h.Handler.WithGroup(name)}
}
