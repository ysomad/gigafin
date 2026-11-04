package api_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/api"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
)

type client struct {
	t   *testing.T
	srv *httptest.Server
}

func (c client) do(method, path, token, body string) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(
		c.t.Context(),
		method,
		c.srv.URL+api.PathPrefix+path,
		strings.NewReader(body),
	)
	require.NoError(c.t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.srv.Client().Do(req)
	require.NoError(c.t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(c.t, err)
	var out map[string]any
	if len(raw) > 0 {
		require.NoError(c.t, json.Unmarshal(raw, &out), string(raw))
	}
	return resp.StatusCode, out
}

func TestAPI(t *testing.T) {
	t.Parallel()
	svc, err := service.New(repotest.New(t), timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	a := svc.Auth
	h, err := api.New(svc, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(api.PathPrefix+"/", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := client{t: t, srv: srv}
	ctx := t.Context()

	u, err := a.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	writeKey, _, err := a.CreateAPIKey(ctx, u.ID, "password1234", "w", domain.ScopeWrite, "never")
	require.NoError(t, err)
	readKey, _, err := a.CreateAPIKey(ctx, u.ID, "password1234", "r", domain.ScopeRead, "never")
	require.NoError(t, err)

	for _, tt := range []struct {
		name       string
		giveMethod string
		giveToken  string
		wantStatus int
		wantCode   string
	}{
		{name: "no key", giveMethod: http.MethodGet, wantStatus: http.StatusUnauthorized, wantCode: "unauthorized"},
		{
			name: "bad key", giveMethod: http.MethodGet, giveToken: "gf_nope_nope",
			wantStatus: http.StatusUnauthorized, wantCode: "unauthorized",
		},
		{
			name: "read key writes", giveMethod: http.MethodPost, giveToken: readKey,
			wantStatus: http.StatusForbidden, wantCode: "forbidden",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := client{t: t, srv: srv}
			status, body := c.do(tt.giveMethod, "/accounts", tt.giveToken, `{"name":"Card","currency":"RUB"}`)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantCode, body["code"])
		})
	}

	status, acc := c.do(http.MethodPost, "/accounts", writeKey, `{"name":"Card","currency":"RUB"}`)
	require.Equal(t, http.StatusCreated, status, acc)
	assert.Equal(t, "0.00", acc["balance"])

	status, body := c.do(http.MethodPost, "/accounts", writeKey, `{"name":"Card","currency":"RUB"}`)
	assert.Equal(t, http.StatusConflict, status, body)

	status, food := c.do(http.MethodPost, "/categories", writeKey, `{"name":"Food","kind":"expense"}`)
	require.Equal(t, http.StatusCreated, status, food)
	status, groceries := c.do(http.MethodPost, "/categories", writeKey,
		`{"name":"Groceries","parent_id":`+num(t, food["id"])+`}`)
	require.Equal(t, http.StatusCreated, status, groceries)

	status, body = c.do(http.MethodPost, "/operations", writeKey, `{"type":"expense","account_id":`+num(t, acc["id"])+
		`,"date":"2026-10-31","category_id":`+num(t, groceries["id"])+`,"amount":"10.123"}`)
	assert.Equal(t, http.StatusBadRequest, status, body)

	status, body = c.do(http.MethodPost, "/operations", writeKey, `{"type":"bogus"}`)
	assert.Equal(t, http.StatusBadRequest, status, body)
	assert.Equal(t, "bad_request", body["code"])

	status, body = c.do(http.MethodPost, "/accounts", writeKey,
		`{"name":"`+strings.Repeat("x", 2<<20)+`","currency":"RUB"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, status, "body over the limit")
	assert.Equal(t, "bad_request", body["code"])

	status, op := c.do(http.MethodPost, "/operations", writeKey, `{"type":"expense","account_id":`+num(t, acc["id"])+
		`,"date":"2026-10-31","time":"12:30","payee":"Shop","category_id":`+num(t, groceries["id"])+`,"amount":"12.5"}`)
	require.Equal(t, http.StatusCreated, status, op)
	assert.Equal(t, "12.50", op["amount"])
	assert.Equal(t, "2026-10-31", op["date"])
	assert.Equal(t, groceries["id"], op["category_id"])
	assert.NotContains(t, op, "origin_id")
	assert.Equal(t, []any{}, op["tags"])
	assert.NotContains(t, op, "updated_at", "not set on create")

	status, split := c.do(http.MethodPost, "/operations/"+num(t, op["id"])+"/split", writeKey,
		`{"parts":[{"category_id":`+num(t, groceries["id"])+`,"amount":"10.5"},{"category_id":`+num(t, food["id"])+
			`,"amount":"2","note":"gum"}]}`)
	require.Equal(t, http.StatusOK, status, split)
	parts, ok := split["items"].([]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	first, ok := parts[0].(map[string]any)
	require.True(t, ok)
	second, ok := parts[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "10.50", first["amount"])
	assert.Equal(t, op["id"], first["origin_id"])
	assert.Equal(t, op["id"], second["origin_id"])
	assert.Equal(t, "gum", second["note"])

	status, body = c.do(http.MethodPost, "/operations/"+num(t, op["id"])+"/split", readKey,
		`{"parts":[{"category_id":1,"amount":"1"},{"category_id":1,"amount":"1"}]}`)
	assert.Equal(t, http.StatusForbidden, status, body)

	status, list := c.do("QUERY", "/operations", readKey, `{"page_size":10}`)
	require.Equal(t, http.StatusOK, status, list)
	assert.Len(t, list["items"], 2)
	status, tagged := c.do(http.MethodPut, "/operations/"+num(t, second["id"]), writeKey,
		`{"type":"expense","account_id":`+num(t, acc["id"])+`,"date":"2026-10-31","category_id":`+num(t, food["id"])+
			`,"amount":"2","tags":["trip"]}`)
	require.Equal(t, http.StatusOK, status, tagged)
	assert.Equal(t, []any{"trip"}, tagged["tags"])
	assert.Contains(t, tagged, "updated_at")
	status, list = c.do("QUERY", "/operations", readKey, `{"tag":"trip"}`)
	require.Equal(t, http.StatusOK, status, list)
	assert.Len(t, list["items"], 1)
	status, list = c.do("QUERY", "/operations", readKey, `{"amount_min":"5"}`)
	require.Equal(t, http.StatusOK, status, list)
	assert.Len(t, list["items"], 1)
	status, list = c.do("QUERY", "/operations", readKey, `{"amount_min":"2","amount_max":"2"}`)
	require.Equal(t, http.StatusOK, status, list)
	assert.Len(t, list["items"], 1)
	status, body = c.do("QUERY", "/operations", readKey, `{"amount_min":"5","amount_max":"2"}`)
	assert.Equal(t, http.StatusBadRequest, status, body)

	status, got := c.do(http.MethodGet, "/accounts/"+num(t, acc["id"]), readKey, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "-12.50", got["balance"])

	status, _ = c.do(http.MethodPut, "/plans", writeKey,
		`{"category_id":`+num(t, food["id"])+`,"month":"2026-10","currency":"RUB","amount":"500"}`)
	assert.Equal(t, http.StatusNoContent, status)
	status, plans := c.do(http.MethodGet, "/plans?month=2026-10", readKey, "")
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, plans["items"], 1)

	status, _ = c.do(http.MethodDelete, "/operations/"+num(t, op["id"]), writeKey, "")
	assert.Equal(t, http.StatusNoContent, status)
	status, body = c.do(http.MethodGet, "/operations/"+num(t, op["id"]), readKey, "")
	assert.Equal(t, http.StatusNotFound, status, body)
}

func num(t *testing.T, v any) string {
	t.Helper()
	f, ok := v.(float64)
	require.True(t, ok, "id %v is not a number", v)
	return strconv.FormatInt(int64(f), 10)
}

func TestAPIRules(t *testing.T) {
	t.Parallel()
	svc, err := service.New(repotest.New(t), timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	h, err := api.New(svc, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle(api.PathPrefix+"/", h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := client{t: t, srv: srv}
	ctx := t.Context()

	u, err := svc.Auth.CreateUser(ctx, "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	key, _, err := svc.Auth.CreateAPIKey(ctx, u.ID, "password1234", "w", domain.ScopeWrite, "never")
	require.NoError(t, err)
	readKey, _, err := svc.Auth.CreateAPIKey(ctx, u.ID, "password1234", "r", domain.ScopeRead, "never")
	require.NoError(t, err)

	status, acc := c.do(http.MethodPost, "/accounts", key, `{"name":"Card","currency":"RUB"}`)
	require.Equal(t, http.StatusCreated, status, acc)
	status, cafe := c.do(http.MethodPost, "/categories", key, `{"name":"Cafe","kind":"expense"}`)
	require.Equal(t, http.StatusCreated, status, cafe)

	status, settings := c.do(http.MethodGet, "/rules/settings", readKey, "")
	require.Equal(t, http.StatusOK, status, settings)
	assert.Equal(t, true, settings["learning"])
	status, _ = c.do(http.MethodPut, "/rules/settings", key, `{"learning":false}`)
	require.Equal(t, http.StatusNoContent, status)

	status, body := c.do(http.MethodPost, "/rules", key,
		`{"conditions":[{"field":"payee","op":"between","values":["a"]}],"actions":[{"type":"add_tag","value":"x"}]}`)
	assert.Equal(t, http.StatusBadRequest, status, body)

	status, rule := c.do(http.MethodPost, "/rules", key,
		`{"conditions":[{"field":"payee","op":"contains","values":["coffee"]}],`+
			`"actions":[{"type":"set_category","value":"`+num(t, cafe["id"])+`"}]}`)
	require.Equal(t, http.StatusCreated, status, rule)
	assert.Equal(t, "default", rule["stage"])
	assert.Equal(t, "all", rule["match"])
	assert.Equal(t, "user", rule["origin"])
	id := num(t, rule["id"])

	status, op := c.do(http.MethodPost, "/operations", key, `{"type":"expense","account_id":`+num(t, acc["id"])+
		`,"date":"2026-10-31","payee":"Coffee Like","amount":"5"}`)
	require.Equal(t, http.StatusCreated, status, op)
	assert.Equal(t, cafe["id"], op["category_id"], "the rule fills the category")

	status, list := c.do(http.MethodGet, "/rules", readKey, "")
	require.Equal(t, http.StatusOK, status, list)
	assert.Len(t, list["items"], 1)

	status, rule = c.do(http.MethodPut, "/rules/"+id, key,
		`{"stage":"post","match":"any","conditions":[{"field":"amount","op":"gt","values":["1"]}],`+
			`"actions":[{"type":"add_tag","value":"big"}]}`)
	require.Equal(t, http.StatusOK, status, rule)
	assert.Equal(t, "post", rule["stage"])
	assert.Contains(t, rule, "updated_at")

	status, res := c.do(http.MethodPost, "/rules/"+id+"/apply", key, "")
	require.Equal(t, http.StatusOK, status, res)
	assert.InDelta(t, 1, res["changed"], 0)

	status, res = c.do(http.MethodPost, "/rules/run", key, `{}`)
	require.Equal(t, http.StatusOK, status, res)
	assert.InDelta(t, 0, res["changed"], 0, "already tagged")

	status, got := c.do(http.MethodGet, "/operations/"+num(t, op["id"]), readKey, "")
	require.Equal(t, http.StatusOK, status, got)
	assert.Equal(t, []any{"big"}, got["tags"])

	status, body = c.do(http.MethodPost, "/rules/run", readKey, `{}`)
	assert.Equal(t, http.StatusForbidden, status, body)

	status, _ = c.do(http.MethodDelete, "/rules/"+id, key, "")
	assert.Equal(t, http.StatusNoContent, status)
	status, body = c.do(http.MethodGet, "/rules/"+id, key, "")
	assert.Equal(t, http.StatusNotFound, status, body)
}
