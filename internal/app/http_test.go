package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/logx"
)

func TestProtect(t *testing.T) {
	t.Parallel()

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := protect(ok, false /* https */)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/accounts", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusForbidden, rec.Code, "cross-origin post")

	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/accounts", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "same-origin post")
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	assert.NotEmpty(t, rec.Header().Get("Permissions-Policy"))
	assert.Empty(t, rec.Header().Get("Strict-Transport-Security"), "no HSTS over http")

	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()
	protect(ok, true /* https */).ServeHTTP(rec, req)
	assert.Equal(t, "max-age=31536000; includeSubDomains", rec.Header().Get("Strict-Transport-Security"))
}

func TestLogRequests(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(logx.Handler{Handler: slog.NewJSONHandler(&buf, nil)})
	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logx.Add(r.Context(), slog.Int64("user_id", 7))
		w.WriteHeader(http.StatusCreated)
		_, err := w.Write([]byte("hello"))
		assert.NoError(t, err)
	}), log)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/accounts?q=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	id := rec.Header().Get("X-Request-Id")
	require.NotEmpty(t, id)
	var line map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &line))
	assert.Equal(t, "http request", line["msg"])
	assert.Equal(t, "INFO", line["level"])
	assert.Equal(t, id, line["request_id"])
	assert.InDelta(t, 7, line["user_id"], 0)
	assert.Equal(t, "POST", line["method"])
	assert.Equal(t, "/accounts", line["path"], "no query string")
	assert.InDelta(t, http.StatusCreated, line["status"], 0)
	assert.InDelta(t, 5, line["bytes"], 0)
	assert.NotContains(t, buf.String(), "secret")
}

func TestLogRequests_Static(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte("body{}"))
		assert.NoError(t, err)
	}), log)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/static/app.css", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Empty(t, buf.String(), "static assets log at debug")
}

func TestLogRequests_Panic(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(logx.Handler{Handler: slog.NewJSONHandler(&buf, nil)})
	h := logRequests(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), log)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	id := rec.Header().Get("X-Request-Id")
	assert.Contains(t, rec.Body.String(), id)
	dec := json.NewDecoder(&buf)
	var panicLine, accessLine map[string]any
	require.NoError(t, dec.Decode(&panicLine))
	require.NoError(t, dec.Decode(&accessLine))
	assert.Equal(t, "ERROR", panicLine["level"])
	assert.Equal(t, "boom", panicLine["panic"])
	assert.Equal(t, id, panicLine["request_id"])
	assert.InDelta(t, http.StatusInternalServerError, accessLine["status"], 0)
}
