package logx_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/logx"
)

func TestHandler(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(logx.Handler{slog.NewJSONHandler(&buf, nil)}).With(slog.String("job", "x"))

	ctx := logx.Start(t.Context(), "req1")
	logx.Add(ctx, slog.Int64("user_id", 7))
	log.InfoContext(ctx, "hello")

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "req1", got["request_id"])
	assert.InDelta(t, 7, got["user_id"], 0)
	assert.Equal(t, "x", got["job"], "attrs of With are kept")
	assert.Equal(t, "req1", logx.RequestID(ctx))

	buf.Reset()
	log.InfoContext(ctx, "again", slog.Int64("user_id", 8))
	assert.Equal(t, 1, bytes.Count(buf.Bytes(), []byte(`"user_id"`)), "no duplicate key")
	assert.Contains(t, buf.String(), `"user_id":8`, "the record's value wins")
}

func TestHandler_NoRequest(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := slog.New(logx.Handler{slog.NewJSONHandler(&buf, nil)})

	logx.Add(t.Context(), slog.Int64("user_id", 7))
	log.InfoContext(t.Context(), "hello")

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.NotContains(t, got, "request_id")
	assert.NotContains(t, got, "user_id")
	assert.Empty(t, logx.RequestID(t.Context()))
}
