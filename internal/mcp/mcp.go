// Package mcp serves the public API as MCP tools over streamable HTTP, authenticated
// with API keys. Every operation of the OpenAPI spec becomes a tool named by its
// operationId; a call is served in process by the API handler, so validation, scopes
// and errors stay in one place.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/ogen-go/ogen"

	"github.com/ysomad/gigafin/internal/api"
	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/logx"
	"github.com/ysomad/gigafin/internal/service"
)

const (
	// Path is where the MCP server is mounted.
	Path     = "/mcp"
	_maxBody = 1 << 20
	// _keyExtra is the auth.TokenInfo.Extra entry holding the authenticated domain.APIKey.
	_keyExtra = "key"
	// _bodyArg is the tool argument holding the request body of an operation.
	_bodyArg = "body"
)

// New returns the MCP server as an http.Handler to mount at Path. Its tools are the
// operations of spec, the OpenAPI document of apiHandler.
func New(a *service.Auth, apiHandler http.Handler, spec []byte, log *slog.Logger) (http.Handler, error) {
	s, err := ogen.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("parse openapi spec: %w", err)
	}
	defs, err := json.Marshal(s.Components.Schemas)
	if err != nil {
		return nil, fmt.Errorf("marshal schemas: %w", err)
	}

	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "gigafin", Version: s.Info.Version}, nil)
	for _, path := range slices.Sorted(maps.Keys(s.Paths)) {
		item := s.Paths[path]
		for method, op := range map[string]*ogen.Operation{
			http.MethodGet:    item.Get,
			"QUERY":           item.Query,
			http.MethodPost:   item.Post,
			http.MethodPut:    item.Put,
			http.MethodPatch:  item.Patch,
			http.MethodDelete: item.Delete,
		} {
			if op == nil {
				continue
			}
			t, o, err := newOperation(s, defs, method, path, item, op)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op.OperationID, err)
			}
			o.api = apiHandler
			srv.AddTool(t, o.call)
		}
	}

	mcpHandler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv },
		&sdkmcp.StreamableHTTPOptions{
			// no session state to keep: every request carries its key
			Stateless:    true,
			JSONResponse: true,
			// the server listens on loopback behind Caddy, which forwards the public Host;
			// DNS rebinding gains nothing, as every request needs a bearer key
			DisableLocalhostProtection: true,
			MaxRequestBodyBytes:        _maxBody,
			Logger:                     log,
		})
	// the SDK writes a verifier error into the response, so internal errors are logged
	// and answered without detail
	verify := func(ctx context.Context, tok string, _ *http.Request) (*auth.TokenInfo, error) {
		key, err := a.AuthenticateAPIKey(ctx, tok, domain.ScopeRead)
		if errors.Is(err, errors.ErrUnauthorized) {
			return nil, fmt.Errorf("invalid or missing api key: %w", auth.ErrInvalidToken)
		}
		if err != nil {
			log.ErrorContext(ctx, "authenticate api key", slog.Any("error", err))
			return nil, errors.Error("internal error")
		}
		logx.Add(ctx, slog.Int64("user_id", key.UserID), slog.Int64("key_id", key.ID))
		return &auth.TokenInfo{
			Scopes: []string{string(key.Scope)},
			UserID: strconv.FormatInt(key.UserID, 10),
			Extra:  map[string]any{_keyExtra: key},
		}, nil
	}
	return auth.RequireBearerToken(verify, &auth.RequireBearerTokenOptions{
		// keys live until revoked
		AllowMissingExpiration: true,
	})(mcpHandler), nil
}

// operation is an API operation served as a tool.
type operation struct {
	api    http.Handler
	method string
	// path is the template under api.PathPrefix, e.g. /accounts/{id}
	path    string
	params  []*ogen.Parameter
	hasBody bool
}

// newOperation returns the tool of op at method and path. Its arguments are the path and
// query parameters and, under body, the request body; defs are the component schemas.
func newOperation(
	s *ogen.Spec, defs []byte, method, path string, item *ogen.PathItem, op *ogen.Operation,
) (*sdkmcp.Tool, operation, error) {
	o := operation{method: method, path: path}
	props := map[string]any{}
	// an empty array, not null: clients reject "required": null
	required := []string{}
	for _, p := range slices.Concat(item.Parameters, op.Parameters) {
		if p.Ref != "" {
			name := strings.TrimPrefix(p.Ref, "#/components/parameters/")
			if p = s.Components.Parameters[name]; p == nil {
				return nil, operation{}, fmt.Errorf("unknown parameter %q", name)
			}
		}
		o.params = append(o.params, p)
		props[p.Name] = p.Schema
		if p.Description != "" {
			props[p.Name] = map[string]any{"allOf": []any{p.Schema}, "description": p.Description}
		}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	if op.RequestBody != nil {
		if m, ok := op.RequestBody.Content["application/json"]; ok {
			o.hasBody = true
			props[_bodyArg] = m.Schema
			if op.RequestBody.Required {
				required = append(required, _bodyArg)
			}
		}
	}
	// tool schemas are standalone documents, so components move to $defs of each
	in, err := json.Marshal(map[string]any{
		"type": "object", "properties": props, "required": required, "$defs": json.RawMessage(defs),
	})
	if err != nil {
		return nil, operation{}, fmt.Errorf("marshal input schema: %w", err)
	}
	// gigafin_ prefix and snake_case, so names stay apart from other servers' tools:
	// listAccounts becomes gigafin_list_accounts
	var name strings.Builder
	name.WriteString("gigafin_")
	for _, r := range op.OperationID {
		if unicode.IsUpper(r) {
			name.WriteByte('_')
			r = unicode.ToLower(r)
		}
		name.WriteRune(r)
	}
	// hints follow HTTP semantics: GET and QUERY read, PUT and DELETE overwrite or remove
	// what is there, POST adds; no tool reaches beyond the user's own data
	readOnly := method == http.MethodGet || method == "QUERY"
	destructive := !readOnly && method != http.MethodPost
	openWorld := false
	t := &sdkmcp.Tool{
		Name:        name.String(),
		Description: strings.TrimSpace(op.Summary + ". " + op.Description),
		InputSchema: json.RawMessage(bytes.ReplaceAll(in, []byte("#/components/schemas/"), []byte("#/$defs/"))),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    readOnly,
			DestructiveHint: &destructive,
			IdempotentHint:  method != http.MethodPost && method != http.MethodPatch,
			OpenWorldHint:   &openWorld,
		},
	}
	for code, resp := range op.Responses {
		m, ok := resp.Content["application/json"]
		if !strings.HasPrefix(code, "2") || !ok || m.Schema == nil {
			continue
		}
		out, err := json.Marshal(map[string]any{
			"type": "object", "allOf": []any{m.Schema}, "$defs": json.RawMessage(defs),
		})
		if err != nil {
			return nil, operation{}, fmt.Errorf("marshal output schema: %w", err)
		}
		t.OutputSchema = json.RawMessage(bytes.ReplaceAll(out, []byte("#/components/schemas/"), []byte("#/$defs/")))
	}
	return t, o, nil
}

// call serves a tool call with an in-process request to the API as the verified key.
func (o operation) call(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
	// every request passes verify, so a missing key is a wiring bug; fail closed
	var key domain.APIKey
	ok := false
	if req.Extra != nil && req.Extra.TokenInfo != nil {
		key, ok = req.Extra.TokenInfo.Extra[_keyExtra].(domain.APIKey)
	}
	if !ok {
		return nil, errors.ErrUnauthorized
	}

	args := map[string]json.RawMessage{}
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			//nolint:nilerr // the model gets bad arguments back as a tool error, to retry
			return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{
				&sdkmcp.TextContent{Text: "arguments: must be a JSON object of the input schema properties"},
			}}, nil
		}
	}
	target, query := o.path, url.Values{}
	for _, p := range o.params {
		raw, ok := args[p.Name]
		if !ok {
			if p.Required {
				return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{
					&sdkmcp.TextContent{Text: p.Name + ": required, see the input schema"},
				}}, nil
			}
			continue
		}
		// parameters are scalars: strings unquoted, numbers and booleans as written
		var v string
		if json.Unmarshal(raw, &v) != nil {
			v = string(raw)
		}
		if p.In == "path" {
			target = strings.ReplaceAll(target, "{"+p.Name+"}", url.PathEscape(v))
		} else {
			query.Set(p.Name, v)
		}
	}
	target = api.PathPrefix + target
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var body []byte
	if o.hasBody {
		body = args[_bodyArg]
	}

	r, err := http.NewRequestWithContext(api.WithKey(ctx, key), o.method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	// the API trusts the key in the context, but ogen still requires the header
	r.Header.Set("Authorization", "Bearer mcp")
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	o.api.ServeHTTP(rec, r)

	if rec.Code >= http.StatusMultipleChoices {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(rec.Body.Bytes(), &e) != nil || e.Message == "" {
			e.Message = http.StatusText(rec.Code)
		}
		return &sdkmcp.CallToolResult{
			IsError: true,
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: e.Message}},
		}, nil
	}
	if rec.Body.Len() == 0 {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "done"}}}, nil
	}
	return &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.TextContent{Text: rec.Body.String()}},
		StructuredContent: json.RawMessage(rec.Body.Bytes()),
	}, nil
}
