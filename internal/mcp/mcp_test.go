package mcp_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/api"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/mcp"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

// bearer adds an Authorization header to every request.
type bearer struct {
	token string
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCP(t *testing.T) {
	t.Parallel()

	svc, err := service.New(repotest.New(t), timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	log := slog.New(slog.DiscardHandler)
	apiHandler, err := api.New(svc, log)
	require.NoError(t, err)
	spec, err := os.ReadFile("../../api/public.yaml")
	require.NoError(t, err)
	h, err := mcp.New(svc.Auth, apiHandler, spec, log)
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	ctx := t.Context()

	u, err := svc.Auth.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	readKey, _, err := svc.Auth.CreateAPIKey(ctx, u.ID, "password1234", "r", domain.ScopeRead, "never")
	require.NoError(t, err)
	writeKey, _, err := svc.Auth.CreateAPIKey(ctx, u.ID, "password1234", "w", domain.ScopeWrite, "never")
	require.NoError(t, err)

	connect := func(token string) (*sdkmcp.ClientSession, error) {
		c := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
		return c.Connect(ctx, &sdkmcp.StreamableClientTransport{
			Endpoint:   srv.URL,
			HTTPClient: &http.Client{Transport: bearer{token: token}},
		}, nil)
	}
	call := func(s *sdkmcp.ClientSession, name string, args any) (map[string]any, string) {
		t.Helper()
		res, err := s.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		if res.IsError {
			require.NotEmpty(t, res.Content)
			text, ok := res.Content[0].(*sdkmcp.TextContent)
			require.True(t, ok)
			return nil, text.Text
		}
		raw, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		var out map[string]any
		require.NoError(t, json.Unmarshal(raw, &out))
		return out, ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(`{}`))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "no key")

	_, err = connect("gf_nope_nope")
	require.Error(t, err, "bad key")

	card := map[string]any{"body": map[string]any{"name": "Card", "currency": "RUB"}}

	read, err := connect(readKey)
	require.NoError(t, err)
	t.Cleanup(func() { _ = read.Close() })

	tools, err := read.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Len(t, tools.Tools, 22)
	for _, tool := range tools.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		assert.NotContains(t, string(schema), `"required":null`, tool.Name)
		if tool.Name == "gigafin_delete_operation" {
			assert.False(t, tool.Annotations.ReadOnlyHint)
			assert.True(t, *tool.Annotations.DestructiveHint)
			assert.True(t, tool.Annotations.IdempotentHint)
			assert.False(t, *tool.Annotations.OpenWorldHint)
		}
		if tool.Name == "gigafin_list_operations" {
			assert.True(t, tool.Annotations.ReadOnlyHint)
			assert.False(t, *tool.Annotations.DestructiveHint)
			assert.True(t, tool.Annotations.IdempotentHint)
		}
	}

	out, errText := call(read, "gigafin_list_accounts", map[string]any{})
	require.Empty(t, errText)
	assert.Empty(t, out["items"])

	_, errText = call(read, "gigafin_create_account", card)
	assert.Equal(t, "key scope does not allow this", errText)

	write, err := connect(writeKey)
	require.NoError(t, err)
	t.Cleanup(func() { _ = write.Close() })

	acc, errText := call(write, "gigafin_create_account", card)
	require.Empty(t, errText)
	assert.Equal(t, "0.00", acc["balance"])

	_, errText = call(write, "gigafin_create_account", card)
	assert.Contains(t, errText, "already exists")

	_, errText = call(write, "gigafin_get_account", map[string]any{"id": 999})
	assert.Contains(t, errText, "not found")

	rule, errText := call(write, "gigafin_create_rule", map[string]any{"body": map[string]any{
		"conditions": []any{map[string]any{"field": "payee", "op": "is", "values": []any{"Shop"}}},
		"actions":    []any{map[string]any{"type": "add_tag", "value": "shop"}},
	}})
	require.Empty(t, errText)
	assert.Equal(t, "user", rule["origin"])
	settings, errText := call(read, "gigafin_get_rule_settings", map[string]any{})
	require.Empty(t, errText)
	assert.Equal(t, true, settings["learning"])

	food, errText := call(write, "gigafin_create_category",
		map[string]any{"body": map[string]any{"name": "Food", "kind": "expense"}})
	require.Empty(t, errText)

	op, errText := call(write, "gigafin_create_operation", map[string]any{"body": map[string]any{
		"type": "expense", "account_id": acc["id"], "date": "2026-10-31", "category_id": food["id"], "amount": "12.50",
	}})
	require.Empty(t, errText)
	assert.Equal(t, "12.50", op["amount"])
	assert.Equal(t, "2026-10-31", op["date"])

	_, errText = call(write, "gigafin_create_operation", map[string]any{"body": map[string]any{
		"type": "expense", "account_id": acc["id"], "date": "31.10.2026", "amount": "1",
	}})
	assert.Contains(t, errText, "date")

	_, errText = call(write, "gigafin_get_operation", map[string]any{})
	assert.Equal(t, "id: required, see the input schema", errText)

	op, errText = call(write, "gigafin_update_operation", map[string]any{"id": op["id"], "body": map[string]any{
		"type": "expense", "account_id": acc["id"], "date": "2026-10-30", "payee": "Shop",
		"category_id": food["id"], "amount": "10",
	}})
	require.Empty(t, errText)
	assert.Equal(t, "Shop", op["payee"])

	ops, errText := call(read, "gigafin_list_operations", map[string]any{"body": map[string]any{
		"date_from": "2026-10-01", "page_size": 10,
	}})
	require.Empty(t, errText)
	assert.Len(t, ops["items"], 1)

	_, errText = call(write, "gigafin_set_plan", map[string]any{"body": map[string]any{
		"category_id": food["id"], "month": "2026-10", "currency": "RUB", "amount": "100",
	}})
	require.Empty(t, errText)
	plans, errText := call(read, "gigafin_list_plans", map[string]any{"month": "2026-10"})
	require.Empty(t, errText)
	assert.Len(t, plans["items"], 1)

	_, errText = call(write, "gigafin_delete_operation", map[string]any{"id": op["id"]})
	require.Empty(t, errText)
	ops, errText = call(read, "gigafin_list_operations", map[string]any{"body": map[string]any{}})
	require.Empty(t, errText)
	assert.Empty(t, ops["items"])
}
