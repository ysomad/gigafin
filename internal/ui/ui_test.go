package ui_test

import (
	"bytes"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
	"github.com/ysomad/gigafin/internal/pagination"
	"github.com/ysomad/gigafin/internal/repository/repotest"
	"github.com/ysomad/gigafin/internal/service"
	"github.com/ysomad/gigafin/internal/timex"
	"github.com/ysomad/gigafin/internal/ui"
)

type browser struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
}

func newBrowser(t *testing.T) (browser, *service.Services, int64) {
	t.Helper()
	svc, err := service.New(repotest.New(t), timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	u, err := ui.New(ui.Config{
		Services: svc, Log: slog.New(slog.DiscardHandler), SessionTTL: 30 * 24 * time.Hour, MaxUploadSize: 10 << 20,
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	require.NoError(t, u.Register(mux))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	user, err := svc.Auth.CreateUser(t.Context(), "alice", "password1234", "Europe/Moscow", "user")
	require.NoError(t, err)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := srv.Client()
	client.Jar = jar
	return browser{t: t, srv: srv, client: client}, svc, user.ID
}

func (b browser) get(path string) (int, string) {
	b.t.Helper()
	req, err := http.NewRequestWithContext(b.t.Context(), http.MethodGet, b.srv.URL+path, nil)
	require.NoError(b.t, err)
	return b.do(req)
}

func (b browser) post(path string, form url.Values) (int, string) {
	b.t.Helper()
	return b.postFrom(path, path, form)
}

// postFrom posts a form as if submitted from the page at from.
func (b browser) postFrom(path, from string, form url.Values) (int, string) {
	b.t.Helper()
	req, err := http.NewRequestWithContext(b.t.Context(), http.MethodPost, b.srv.URL+path,
		strings.NewReader(form.Encode()))
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", b.srv.URL+from)
	return b.do(req)
}

// upload posts the new import form as multipart from its file step. An empty file
// leaves the file field out.
func (b browser) upload(fields map[string]string, file string) (int, string) {
	b.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(b.t, mw.WriteField(k, v))
	}
	if file != "" {
		fw, err := mw.CreateFormFile("file", "tbank.csv")
		require.NoError(b.t, err)
		_, err = fw.Write([]byte(file))
		require.NoError(b.t, err)
	}
	require.NoError(b.t, mw.Close())
	req, err := http.NewRequestWithContext(b.t.Context(), http.MethodPost, b.srv.URL+"/imports", &buf)
	require.NoError(b.t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Referer", b.srv.URL+"/imports/new?source=tbank")
	return b.do(req)
}

func (b browser) do(req *http.Request) (int, string) {
	b.t.Helper()
	resp, err := b.client.Do(req)
	require.NoError(b.t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(b.t, err)
	return resp.StatusCode, string(body)
}

func (b browser) login() {
	b.t.Helper()
	status, body := b.post("/login", url.Values{"login": {"alice"}, "password": {"password1234"}})
	require.Equal(b.t, http.StatusOK, status, body)
}

func TestLoginRequired(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)

	status, body := b.get("/operations")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Sign in", "redirected to login")

	status, body = b.post("/login", url.Values{"login": {"alice"}, "password": {"wrong"}})
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `<p class="error" role="alert">invalid login or password</p>`,
		"a business error is shown on its form")
	assert.NotContains(t, body, `class="toast"`)

	b.login()
	status, body = b.get("/")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Dashboard")
	assert.Contains(t, body, "No operations yet")
	status, body = b.get("/operations")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "No operations yet")
}

func TestPagesAndFlows(t *testing.T) {
	t.Parallel()

	b, f, userID := newBrowser(t)
	b.login()

	status, body := b.post("/accounts", url.Values{"name": {"Card"}, "currency": {"RUB"}, "balance": {"1 000,50"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "1\u202f001<", "minor units hidden and rounded by default")
	assert.NotContains(t, body, "data-minor-units")
	cards, err := f.Accounts.List(t.Context(), userID)
	require.NoError(t, err)
	status, body = b.get("/accounts/" + strconv.FormatInt(cards[0].ID, 10))
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="1000.50"`, "inputs keep non-zero minor units when they are hidden")

	status, body = b.post("/settings", url.Values{"timezone": {"Europe/Moscow"}, "show_minor_units": {"on"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `name="show_minor_units" checked`)
	status, body = b.get("/accounts")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "1\u202f000.50")
	assert.Contains(t, body, "data-minor-units")

	status, body = b.post("/accounts", url.Values{"name": {"Card"}, "currency": {"RUB"}})
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, "already exists")

	status, _ = b.post("/categories", url.Values{"kind": {"expense"}, "name": {"Food"}})
	require.Equal(t, http.StatusOK, status)
	tree, err := f.Categories.Tree(t.Context(), userID)
	require.NoError(t, err)
	food := tree[0]
	status, _ = b.post("/categories", url.Values{"parent": {strconv.FormatInt(food.ID, 10)}, "name": {"Groceries"}})
	require.Equal(t, http.StatusOK, status)
	status, _ = b.post("/categories", url.Values{"parent": {strconv.FormatInt(food.ID, 10)}, "name": {"Cafe"}})
	require.Equal(t, http.StatusOK, status)
	tree, err = f.Categories.Tree(t.Context(), userID)
	require.NoError(t, err)
	cafe, groceries := tree[0].Children[0], tree[0].Children[1]
	status, _ = b.post("/categories", url.Values{"kind": {"income"}, "name": {"Salary"}})
	require.Equal(t, http.StatusOK, status)
	tree, err = f.Categories.Tree(t.Context(), userID)
	require.NoError(t, err)
	salary := tree[1]

	// a tampered id is a form error shown on the page, not a silent 0
	status, body = b.postFrom("/categories", "/categories/new", url.Values{"parent": {"abc"}, "name": {"Bad"}})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "invalid integer value")
	tree, err = f.Categories.Tree(t.Context(), userID)
	require.NoError(t, err)
	assert.Len(t, tree, 2)

	accounts, err := f.Accounts.List(t.Context(), userID)
	require.NoError(t, err)
	card := accounts[0]

	status, body = b.get("/operations/new?type=expense")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `name="category"`)
	assert.NotContains(t, body, `name="split_category"`, "only saved operations are split")

	status, body = b.get("/operations/split?type=expense")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Groceries")
	assert.NotContains(t, body, "<html")

	status, body = b.post("/operations", url.Values{
		"type": {"expense"}, "account": {strconv.FormatInt(card.ID, 10)}, "date": {"2026-10-31"}, "payee": {"Market"},
		"category": {strconv.FormatInt(groceries.ID, 10)}, "amount": {"150,25"},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Market")
	assert.Contains(t, body, `-150.25 <span class="text-light">RUB</span>`)

	status, body = b.post("/operations", url.Values{
		"type": {"expense"}, "account": {strconv.FormatInt(card.ID, 10)}, "date": {"2026-10-31"},
		"category": {strconv.FormatInt(salary.ID, 10)}, "amount": {"10"},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(
		t,
		body,
		`<p class="error" role="alert">category: category &#34;Salary&#34; is not an expense category</p>`,
		"a business error is shown on its form",
	)
	assert.NotContains(t, body, `class="toast"`, "an error shown on its form is not a toast")

	status, body = b.post("/operations", url.Values{
		"type": {"expense"}, "account": {strconv.FormatInt(card.ID, 10)}, "date": {"31.10.2026"}, "amount": {"ten"},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	for _, e := range []string{
		`<div id="category-error-expense" class="error" role="status">required</div>`,
		`<div id="amount-error-expense" class="error" role="status">enter a number</div>`,
		`<div id="date-error-expense" class="error" role="status">must be YYYY-MM-DD</div>`,
	} {
		assert.Contains(t, body, e, "every invalid input is shown at once")
	}

	ops, _, err := f.Operations.List(t.Context(), userID, domain.Filter{Type: domain.OpExpense}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 1)
	opPath := "/operations/" + strconv.FormatInt(ops[0].ID, 10)
	status, body = b.get(opPath + "/edit")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `value="150.25"`)
	assert.Contains(t, body, `href="`+opPath+`/split"`)
	status, body = b.get(opPath + "/split")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `name="split_category"`)

	status, body = b.post(opPath+"/split", url.Values{
		"split_category": {strconv.FormatInt(groceries.ID, 10), strconv.FormatInt(cafe.ID, 10)},
		"split_amount":   {"100", "1"},
		"split_note":     {"", ""},
	})
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(
		t,
		body,
		`<p class="error" role="alert">parts add up to 101.00, not 150.25</p>`,
		"an error of no input is shown on the form",
	)
	assert.Contains(t, body, `value="100"`, "rows are kept")

	status, body = b.post(opPath+"/split", url.Values{
		"split_category": {strconv.FormatInt(groceries.ID, 10), strconv.FormatInt(cafe.ID, 10), ""},
		"split_amount":   {"100,25", "50", ""},
		"split_note":     {"", "coffee", ""},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Food / Groceries · split")
	assert.Contains(t, body, "Food / Cafe · split")
	assert.Contains(t, body, "coffee")

	ops, _, err = f.Operations.List(t.Context(), userID, domain.Filter{Type: domain.OpExpense}, pagination.Params{})
	require.NoError(t, err)
	require.Len(t, ops, 2)
	status, body = b.get(opPath + "/split")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Part of a split")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		b.srv.URL+"/settings/export?format=csv&from=2026-10-31&to=2026-10-31", nil)
	require.NoError(t, err)
	resp, err := b.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	csv, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(csv))
	assert.Equal(t, "text/csv; charset=utf-8", resp.Header.Get("Content-Type"))
	assert.Contains(t, resp.Header.Get("Content-Disposition"), `attachment; filename="gigafin-`)
	lines := strings.Split(strings.TrimSpace(string(csv)), "\n")
	require.Len(t, lines, 3, "header and the two parts")
	assert.Equal(t, "2026-10-31;;Card;-100.25;RUB;Food / Groceries;Market;;;;"+
		strconv.FormatInt(ops[0].OriginID, 10)+";", lines[1])
	status, body = b.get("/settings/export?format=csv&from=2099-01-01")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "date;time;account;amount;currency;category;payee;note;to_account;mcc;origin_id;tags",
		strings.TrimSpace(body), "header only")
	status, body = b.get("/settings/export?format=xlsx")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "unsupported")

	cardPath := "/accounts/" + strconv.FormatInt(card.ID, 10)
	for _, path := range []string{
		"/accounts", "/accounts/new", cardPath,
		"/categories", "/categories/new", "/categories/" + strconv.FormatInt(cafe.ID, 10),
		"/plans", "/plans?month=2026-10", "/plans/new?month=2026-10",
		"/imports", "/imports/new", "/settings",
		"/operations?type=expense&q=Mark", "/operations?min=1&max=1+000,50", "/operations/new?type=transfer",
	} {
		status, body := b.get(path)
		assert.Equal(t, http.StatusOK, status, path+": "+body)
	}

	status, _ = b.get("/operations/999/edit")
	assert.Equal(t, http.StatusNotFound, status)

	status, body = b.post("/plans", url.Values{
		"month": {"2026-10"}, "category": {strconv.FormatInt(food.ID, 10)}, "currency": {"RUB"},
		"amount": {"300"}, "repeating": {"true"},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "149.75", "left of food plan")
	// an empty amount removes only the month's plan, it does not stop carrying
	status, body = b.post("/plans", url.Values{
		"month": {"2026-11"}, "category": {strconv.FormatInt(food.ID, 10)}, "currency": {"RUB"},
		"amount": {""}, "repeating": {"true"},
	})
	require.Equal(t, http.StatusOK, status, body)
	status, body = b.post("/plans/fill", url.Values{"month": {"2026-11"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="300`, "food plan carried into 2026-11")

	status, body = b.post(opPath, url.Values{
		"type": {"expense"}, "account": {strconv.FormatInt(card.ID, 10)}, "date": {"2026-10-31"}, "payee": {"Market"},
		"category": {strconv.FormatInt(groceries.ID, 10)}, "amount": {"100,25"}, "tags": {"trip, food ,"},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `<a class="tag" href="/operations?tag=trip">trip</a>`)
	status, body = b.get(opPath + "/edit")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `value="food, trip"`)
	status, body = b.get("/operations?tags=trip,+nope")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "100.25")
	assert.Contains(t, body, `<input type="hidden" name="tags" value="trip, nope">`)

	status, body = b.get("/?month=2026-10")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Expenses by category")
	assert.Contains(t, body, "Food")
	assert.Contains(t, body, `<figure class="trend" data-currency="RUB">`)
	assert.Contains(t, body, `"months":[`)
	assert.Contains(t, body, "Top payees")
	assert.Contains(t, body, "Market")
	assert.Contains(t, body, "Expenses by month")
	assert.NotContains(t, body, "networth", "net worth is a line of the income vs expense chart")
	assert.Contains(t, body, `"balance":[`)
	assert.Less(t, strings.Index(body, "Expenses by month"), strings.Index(body, "Top payees"), "payees at the bottom")
	assert.Contains(t, body, `<option value="2026-09" selected>`, "compared with the previous month")

	status, body = b.get("/?month=2026-11&compare=2026-10")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `<option value="2026-10" selected>`)
	assert.Contains(t, body, `<td class="num income">-100%</td>`, "food spent in the compare month only")

	foodPath := "/categories/" + strconv.FormatInt(food.ID, 10)
	status, body = b.postFrom(foodPath+"/delete", foodPath, nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "contains")

	// an htmx action of no form leaves the page as it is and gets the error as a toast
	req, err = http.NewRequestWithContext(t.Context(), http.MethodPost, b.srv.URL+foodPath+"/delete", nil)
	require.NoError(t, err)
	req.Header.Set("HX-Request", "true")
	resp, err = b.client.Do(req)
	require.NoError(t, err)
	toast, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, "none", resp.Header.Get("HX-Reswap"))
	assert.True(t, strings.HasPrefix(string(toast), `<div id="toasts" hidden hx-swap-oob="true">`+
		`<output class="toast" data-variant="danger"><p>`), string(toast))
	assert.Contains(t, string(toast), "contains")
	assert.NotContains(t, string(toast), "<html")

	status, body = b.postFrom("/settings/api-keys", "/settings",
		url.Values{"name": {"script"}, "scope": {"read"}, "expires": {"30"}, "password": {"wrong"}})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `<p class="col-12 error" role="alert">wrong password</p>`,
		"key needs the current password, a business error of the form")
	assert.NotContains(t, body, "not shown again")

	status, body = b.post("/settings/api-keys",
		url.Values{"name": {"script"}, "scope": {"read"}, "expires": {"never"}, "password": {"password1234"}})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "gf_")
	assert.Contains(t, body, "be shown again")
	assert.Contains(t, body, "this browser", "current session listed")
	assert.Contains(t, body, `<td data-label="Expires">never</td>`)
}

func TestChangePassword(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)
	b.login()
	status, body := b.postFrom("/settings/password", "/settings",
		url.Values{"current": {""}, "password": {"password5678"}})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `id="current-password-error" class="error" role="status">required<`)
	status, body = b.postFrom("/settings/password", "/settings",
		url.Values{"current": {"wrong"}, "password": {"password5678"}})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `id="current-password-error" class="error" role="status">wrong password<`)
	assert.NotContains(t, body, "invalid login or password", "not the login error")
	assert.Contains(t, body, `name="timezone" value="Europe/Moscow"`, "other forms keep their values")
	status, body = b.postFrom("/settings/password", "/settings",
		url.Values{"current": {"password1234"}, "password": {"password1234"}})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `id="new-password-error" class="error" role="status">must differ from the current password<`)

	// a boosted post keeps the URL of the form
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, b.srv.URL+"/settings/password",
		strings.NewReader(url.Values{"current": {"wrong"}, "password": {"password5678"}}.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Boosted", "true")
	resp, err := b.client.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Equal(t, "false", resp.Header.Get("HX-Push-Url"))
	assert.Equal(t, "outerHTML show:none", resp.Header.Get("HX-Reswap"), "no scroll to the top")
	assert.Equal(t, "#form-password", resp.Header.Get("HX-Retarget"), "just the form is replaced")
	assert.Equal(t, "#form-password", resp.Header.Get("HX-Reselect"))
	assert.Contains(t, body, `<form id="form-password"`)
	assert.NotContains(t, body, `class="toast"`, "shown at the field, not as a toast")

	status, body = b.postFrom("/settings/password", "/settings",
		url.Values{"current": {"password1234"}, "password": {"password5678"}})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `<output class="toast" data-variant="success"><p>Password changed.`)
	_, body = b.get("/settings")
	assert.NotContains(t, body, "Password changed.", "shown once")
}

func TestSettingsHtmx(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)
	b.login()
	hxPost := func(path string, form url.Values) (int, http.Header, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, b.srv.URL+path,
			strings.NewReader(form.Encode()))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("HX-Request", "true")
		resp, err := b.client.Do(req)
		require.NoError(t, err)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp.StatusCode, resp.Header, string(body)
	}

	// a saved setting answers with the page, of which the form swaps in its card
	status, header, body := hxPost("/settings", url.Values{"timezone": {"Europe/Moscow"}, "show_minor_units": {"on"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Empty(t, header.Get("HX-Location"), "no reload of the whole page")
	assert.Contains(t, body, `name="show_minor_units" checked`, "not the settings read before the change")

	status, header, body = hxPost("/settings", url.Values{"timezone": {"Asia/Tokyo"}, "show_minor_units": {"on"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Empty(t, header.Get("HX-Location"))
	assert.Contains(t, body, `name="timezone" value="Asia/Tokyo"`, "not the user read before the change")

	status, header, body = hxPost("/settings/password",
		url.Values{"current": {"password1234"}, "password": {"password5678"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Empty(t, header.Get("HX-Location"))
	assert.Contains(t, body, `<output class="toast" data-variant="success"><p>Password changed.`)
	assert.Contains(t, body, `id="settings-access"`)

	// the whole page changes language, so it is loaded again
	status, header, body = hxPost("/settings", url.Values{"timezone": {"Asia/Tokyo"}, "language": {"ru"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.Equal(t, "/settings", header.Get("HX-Location"))
}

func TestRevokeSession(t *testing.T) {
	t.Parallel()

	b, svc, userID := newBrowser(t)
	b.login()
	other, _, err := svc.Auth.Login(t.Context(), "alice", "password1234", "OtherBrowser/1.0")
	require.NoError(t, err)

	sessions, err := svc.Auth.ListSessions(t.Context(), userID, other)
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	status, body := b.get("/settings")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "OtherBrowser/1.0")

	var otherID int64
	for _, s := range sessions {
		if s.Current {
			otherID = s.ID
		}
	}
	status, body = b.post("/settings/sessions/"+strconv.FormatInt(otherID, 10)+"/revoke", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "OtherBrowser/1.0")
	_, err = svc.Auth.SessionUser(t.Context(), other)
	require.ErrorIs(t, err, errors.ErrUnauthorized)
}

func TestRevokeAPIKey(t *testing.T) {
	t.Parallel()

	b, svc, userID := newBrowser(t)
	b.login()
	_, key, err := svc.Auth.CreateAPIKey(t.Context(), userID, "password1234", "script", domain.ScopeRead, "never")
	require.NoError(t, err)

	status, body := b.get("/settings")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "gf_"+key.Prefix)

	status, body = b.post("/settings/api-keys/"+strconv.FormatInt(key.ID, 10)+"/revoke", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, "gf_"+key.Prefix, "revoked key is not listed")
}

func TestCreditAccount(t *testing.T) {
	t.Parallel()

	b, f, userID := newBrowser(t)
	b.login()

	status, body := b.post("/accounts", url.Values{
		"name": {"Platinum"}, "kind": {"credit"}, "currency": {"RUB"}, "balance": {"-1000"}, "credit_limit": {"50 000"},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Credit and loans")
	assert.Contains(t, body, "49\u202f000<", "available")

	status, body = b.post("/accounts", url.Values{
		"name": {"Cash"}, "kind": {"cash"}, "currency": {"RUB"}, "credit_limit": {"100"},
	})
	require.Equal(t, http.StatusOK, status, "limit ignored for non-credit kinds: "+body)

	accounts, err := f.Accounts.List(t.Context(), userID)
	require.NoError(t, err)
	card := accounts[1]
	require.Equal(t, "Platinum", card.Name)

	cardPath := "/accounts/" + strconv.FormatInt(card.ID, 10)
	status, body = b.get(cardPath)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="50000"`, "inputs drop zero minor units when they are hidden")
	status, _ = b.post("/settings", url.Values{"timezone": {"Europe/Moscow"}, "show_minor_units": {"on"}})
	require.Equal(t, http.StatusOK, status)
	status, body = b.get(cardPath)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="50000.00"`)
	status, body = b.postFrom(cardPath, cardPath, url.Values{
		"name": {"Platinum"}, "kind": {"debit"}, "credit_limit": {"50000"},
	})
	require.Equal(t, http.StatusOK, status, body)
	card, err = f.Accounts.Get(t.Context(), userID, card.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AccountDebit, card.Kind)
	assert.True(t, card.CreditLimit.IsZero())
}

func TestImportUpload(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)
	b.login()

	status, body := b.get("/imports/new")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="tbank"`)
	status, body = b.get("/imports/new?source=tbank")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `name="content"`)

	csv := `"Имя счёта";"Номер карты";"Дата операции";"Сумма операции";"Валюта операции";"Сумма в валюте счёта";` +
		`"Валюта счёта";"Статус";"Категория по-умолчанию";"Ваша категория";"MCC";"Описание";"Сообщение"` + "\n" +
		`"Black";"*1";"29.10.2026 18:48:38";"-1040,00";"RUB";"-1040,00";"RUB";"Ок";"Супермаркеты";"";"";"Самокат";""` + "\n" +
		`"Black";"*1";"29.10.2026 19:00:00";"-50,00";"RUB";"-50,00";"RUB";"FAILED";"Такси";"";"";"Яндекс Go";""` + "\n"

	status, body = b.upload(map[string]string{"source": "tbank"}, csv)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Create new account", "lands on the accounts step")

	status, body = b.get("/imports/1/preview")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Самокат")
	assert.Contains(t, body, `id="skipped"`)
	assert.Contains(t, body, "Яндекс Go")
	assert.Contains(t, body, "status &#34;FAILED&#34;")
	assert.Contains(t, body, "disabled", "import is disabled until mapped")

	status, body = b.post("/imports/1/accounts", url.Values{
		"acc_name": {"Black"}, "acc_currency": {"RUB"}, "acc_id": {"0"}, "acc_new": {"Black"},
		"acc_kind": {"debit"}, "acc_limit": {""},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Create new category", "lands on the categories step")

	status, body = b.post("/imports/1/categories", url.Values{
		"cat_name": {"Супермаркеты"}, "cat_kind": {"expense"}, "cat_id": {"0"},
		"cat_new": {"Food / Groceries"},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Самокат", "lands on the preview step")

	status, body = b.get("/imports/1")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Самокат", "mapped import continues at preview")
	assert.Contains(t, body, "Import 1 row")
	assert.NotContains(t, body, "disabled")

	status, body = b.post("/imports/1/commit", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Undo import")
	assert.Contains(t, body, "Самокат", "committed import still shows its rows")

	status, _ = b.get("/imports/1/commit")
	assert.Equal(t, http.StatusNotFound, status, "commit is no separate step")

	status, body = b.get("/imports/1/accounts")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Undo import", "committed import has no mapping steps")

	status, _ = b.get("/imports/1/nope")
	assert.Equal(t, http.StatusNotFound, status)

	status, body = b.get("/operations")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "-1\u202f040 <span class=\"text-light\">RUB</span>")
	assert.Contains(t, body, "Food / Groceries", `"Parent / Child" created a nested category`)
}

func TestImportPaste(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)
	b.login()

	csv := `"Имя счёта";"Номер карты";"Дата операции";"Сумма операции";"Валюта операции";"Сумма в валюте счёта";` +
		`"Валюта счёта";"Статус";"Категория по-умолчанию";"Ваша категория";"MCC";"Описание";"Сообщение"` + "\n" +
		`"Black";"*1";"29.10.2026 18:48:38";"-1040,00";"RUB";"-1040,00";"RUB";"Ок";"Супермаркеты";"";"";"Самокат";""` + "\n"

	status, body := b.upload(map[string]string{"source": "tbank", "content": csv}, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "pasted.csv")
	assert.Contains(t, body, "Create new account")

	status, body = b.upload(map[string]string{"source": "tbank"}, "")
	require.Equal(t, http.StatusUnprocessableEntity, status, body)
	assert.Contains(t, body,
		`<div id="file-error" class="error" role="status">choose a CSV file or paste its content</div>`)

	status, body = b.upload(map[string]string{"content": csv}, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "choose what to import")
}

func TestImportSources(t *testing.T) {
	t.Parallel()

	b, f, userID := newBrowser(t)
	b.login()

	status, body := b.get("/imports/new")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="zenmoney"`)
	assert.Contains(t, body, `value="gigafin"`)
	status, body = b.get("/imports/new?source=gigafin")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "to_account")

	card, err := f.Accounts.Create(t.Context(), userID, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)
	gigafin := "date;time;account;amount;currency;category;payee;note;to_account\n" +
		"2026-10-29;18:48;Card;-100;RUB;Food;Shop;;\n" +
		"2026-10-29;19:00;Card;-50;RUB;;;;Cash\n"
	status, body = b.upload(map[string]string{"source": "gigafin", "content": gigafin}, "")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `<option value="`+strconv.FormatInt(card.ID, 10)+`" selected>Card</option>`,
		"an account of the same name is preselected")

	status, body = b.get("/imports/1/preview")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Card → Cash")

	zen := "date;categoryName;payee;comment;outcomeAccountName;outcome;outcomeCurrencyShortTitle;" +
		"incomeAccountName;income;incomeCurrencyShortTitle;createdDate;changedDate;qrCode\n" +
		`2026-10-29;"Transport / Taxi";"YANDEX";;"Black";"425";RUB;"Black";"0";RUB;"2026-10-29 22:13:18";;` + "\n"
	status, body = b.upload(map[string]string{"source": "zenmoney"}, zen)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Create new account", "lands on the accounts step")

	status, body = b.upload(map[string]string{"source": "zenmoney", "content": gigafin}, "")
	require.Equal(t, http.StatusUnprocessableEntity, status, body)
	assert.Contains(t, body, "not a Zenmoney CSV export")
	assert.Contains(t, body, "2026-10-29;18:48;Card", "the pasted content is kept")
}

func TestDeleteAllData(t *testing.T) {
	t.Parallel()

	b, f, userID := newBrowser(t)
	b.login()

	_, err := f.Accounts.Create(t.Context(), userID, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)

	status, body := b.get("/settings")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Danger zone")

	status, body = b.postFrom("/settings/delete-data", "/settings", url.Values{"password": {"wrong"}})
	require.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `<p class="col-12 error" role="alert">wrong password</p>`)
	accounts, err := f.Accounts.List(t.Context(), userID)
	require.NoError(t, err)
	assert.Len(t, accounts, 1, "nothing deleted on a wrong password")

	status, body = b.postFrom("/settings/delete-data", "/settings", url.Values{"password": {"password1234"}})
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "No accounts yet", "still signed in, accounts page is empty")
	accounts, err = f.Accounts.List(t.Context(), userID)
	require.NoError(t, err)
	assert.Empty(t, accounts)
}

func TestBackup(t *testing.T) {
	t.Parallel()

	const passphrase = "correct horse battery"

	t.Run("not allowed", func(t *testing.T) {
		t.Parallel()

		b, _, _ := newBrowser(t)
		b.login()
		status, body := b.get("/settings")
		require.Equal(t, http.StatusOK, status)
		assert.NotContains(t, body, "Download backup", "hidden from users not allowed")

		status, body = b.postFrom("/settings/backup", "/settings",
			url.Values{"password": {"password1234"}, "passphrase": {passphrase}})
		require.Equal(t, http.StatusOK, status, "back to the settings page")
		assert.Contains(t, body, `<output class="toast" data-variant="danger"><p>backups are forbidden`)
		assert.NotContains(t, body, "age-encryption.org/v1")
	})

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()

		b, svc, _ := newBrowser(t)
		require.NoError(t, svc.Auth.SetRoleByLogin(t.Context(), "alice", "admin"))
		b.login()

		status, body := b.get("/settings")
		require.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "Download backup")

		status, body = b.postFrom("/settings/backup", "/settings",
			url.Values{"password": {""}, "passphrase": {""}})
		require.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Contains(t, body, `id="backup-password-error" class="error" role="status">required<`)
		assert.Contains(t, body, `id="backup-passphrase-error" class="error" role="status">required<`)

		status, body = b.postFrom("/settings/backup", "/settings",
			url.Values{"password": {"password1234"}, "passphrase": {"short"}})
		require.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Contains(t, body, `id="backup-passphrase-error" class="error" role="status">must be at least`)

		status, body = b.postFrom("/settings/backup", "/settings",
			url.Values{"password": {"wrong"}, "passphrase": {passphrase}})
		require.Equal(t, http.StatusUnprocessableEntity, status)
		assert.Contains(t, body, `id="backup-password-error" class="error" role="status">wrong password<`)
		assert.NotContains(t, body, `class="toast"`, "shown at the field, not as a toast")

		form := url.Values{"password": {"password1234"}, "passphrase": {passphrase}}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, b.srv.URL+"/settings/backup",
			strings.NewReader(form.Encode()))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := b.client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, string(got))
		assert.Regexp(t, `^attachment; filename="gigafin-\d{4}-\d{2}-\d{2}\.db\.age"$`,
			resp.Header.Get("Content-Disposition"))
		assert.True(t, strings.HasPrefix(string(got), "age-encryption.org/v1\n"), "age encrypted")
	})
}

func TestErrorNotFromURL(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)
	b.login()

	_, body := b.get("/accounts?error=Your+account+is+locked")
	assert.NotContains(t, body, "Your account is locked", "messages come from the server only")
}

func TestSecureCookies(t *testing.T) {
	t.Parallel()

	svc, err := service.New(repotest.New(t), timex.System{}, slog.New(slog.DiscardHandler), service.Options{
		Session: domain.SessionLifetime{Max: 30 * 24 * time.Hour, Idle: 7 * 24 * time.Hour},
	})
	require.NoError(t, err)
	_, err = svc.Auth.CreateUser(t.Context(), "alice", "password1234", "UTC", "user")
	require.NoError(t, err)
	u, err := ui.New(ui.Config{
		Services: svc, Log: slog.New(slog.DiscardHandler), SecureCookies: true,
		SessionTTL: 30 * 24 * time.Hour, MaxUploadSize: 10 << 20,
	})
	require.NoError(t, err)
	mux := http.NewServeMux()
	require.NoError(t, u.Register(mux))

	form := url.Values{"login": {"alice"}, "password": {"password1234"}}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	cookies := res.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "__Host-gigafin_session", cookies[0].Name)
	assert.True(t, cookies[0].Secure)
	assert.True(t, cookies[0].HttpOnly)
}

func TestImportRates(t *testing.T) {
	t.Parallel()

	b, svc, userID := newBrowser(t)
	b.login()
	ctx := t.Context()

	status, body := b.get("/imports/new?source=rates")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `action="/imports/rates"`)

	file := "date,from,to,rate\n2026-10-01,USD,RUB,90\n2026-10-03,USD,RUB,91\n"
	_, body = b.postFrom("/imports/rates", "/imports/new?source=rates", url.Values{"content": {file}})
	assert.Contains(t, body, "exchange rates are off", "rates must be on")

	err := svc.Settings.Set(ctx, userID, domain.Settings{RateSource: domain.RateSourceCBR, PrimaryCurrency: "RUB"})
	require.NoError(t, err)
	require.NoError(t, svc.Rates.Set(ctx, userID, "USD", "2026-10-02", "85"))

	status, body = b.postFrom("/imports/rates", "/imports/new?source=rates", url.Values{"content": {file}})
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "3 daily rates · 2 new · 1 change a stored rate")
	assert.Contains(t, body, "<strong>changed</strong>", "the carried 2 October rate replaces 85")
	latest, err := svc.Rates.Latest(ctx, userID)
	require.NoError(t, err)
	require.Len(t, latest, 1)
	assert.Equal(t, "2026-10-02", timex.FormatDate(latest[0].Date), "preview stores nothing")

	status, body = b.postFrom("/imports/rates/commit", "/imports/rates", url.Values{"content": {file}})
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "Imported 3 daily rates.")
	latest, err = svc.Rates.Latest(ctx, userID)
	require.NoError(t, err)
	require.Len(t, latest, 1)
	assert.Equal(t, "2026-10-03", timex.FormatDate(latest[0].Date))
	assert.True(t, latest[0].Manual)
}

func TestLanguage(t *testing.T) {
	t.Parallel()

	b, _, _ := newBrowser(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, b.srv.URL+"/login", nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	_, body := b.do(req)
	assert.Contains(t, body, `<html lang="ru">`, "a signed-out page follows the browser")
	assert.Contains(t, body, "Войти")

	b.login()
	_, body = b.get("/settings")
	assert.Contains(t, body, `<html lang="en">`, "English by default")

	_, body = b.post("/settings", url.Values{"timezone": {"Europe/Moscow"}, "language": {"ru"}})
	assert.Contains(t, body, `<html lang="ru">`)
	assert.Contains(t, body, "Настройки")

	_, body = b.post("/settings", url.Values{"timezone": {"Europe/Moscow"}, "language": {"xx"}})
	assert.Contains(t, body,
		`<div id="language-error" class="error" role="status">выберите из списка</div>`,
		"errors are translated")

	_, body = b.post("/settings", url.Values{"timezone": {"Europe/Moscow"}, "language": {""}})
	assert.Contains(t, body, `<html lang="en">`, "empty follows the browser")
}

func TestRulesFlow(t *testing.T) {
	t.Parallel()

	b, f, userID := newBrowser(t)
	b.login()
	ctx := t.Context()

	acc, err := f.Accounts.Create(ctx, userID, domain.AccountInput{Name: "Card", Currency: "RUB"})
	require.NoError(t, err)
	cafe, err := f.Categories.Create(ctx, userID, 0, domain.KindExpense, "Cafe")
	require.NoError(t, err)
	food, err := f.Categories.Create(ctx, userID, 0, domain.KindExpense, "Food")
	require.NoError(t, err)
	cafeID, accID := strconv.FormatInt(cafe.ID, 10), strconv.FormatInt(acc.ID, 10)
	op, err := f.Operations.Create(ctx, userID, domain.OperationInput{
		Type: domain.OpExpense, AccountID: acc.ID, CategoryID: food.ID, Amount: money.FromInt(300),
		Date: time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC), Payee: "Coffee Like",
	})
	require.NoError(t, err)

	status, body := b.get("/rules")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "No rules yet")
	status, body = b.get("/settings")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `name="auto_category_rules" checked`, "rules are learned by default")

	status, body = b.get("/rules/new?payee=Coffee+Like&category=" + cafeID)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="Coffee Like"`)

	status, body = b.get("/rules/condition?cond_field=category&cond_op=one_of")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `name="cond_value"`)
	assert.Contains(t, body, `value="one_of" selected`)
	assert.Contains(t, body, ">Cafe<")
	status, body = b.get("/rules/action?act_type=set_payee")
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="set_payee" selected`)

	form := url.Values{
		"stage": {"default"}, "match": {"all"},
		"cond_field": {"payee", "amount"}, "cond_op": {"contains", "between"}, "cond_value": {"coffee", "100 | 500"},
		"act_type": {"set_category", "add_tag"}, "act_value": {cafeID, "coffee"},
	}
	status, body = b.post("/rules/preview", form)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Matches 1 operation")

	bad := url.Values{
		"stage": {"default"}, "match": {"all"}, "cond_field": {"amount"}, "cond_op": {"between"},
		"cond_value": {"1"}, "act_type": {"add_tag"}, "act_value": {"x"},
	}
	status, body = b.post("/rules", bad)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Contains(t, body, `<div class="error" role="status">between takes 2 values</div>`,
		"a condition error is shown in its row")

	status, body = b.post("/rules", form)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Payee contains <strong>coffee</strong> and Amount is between <strong>100 | 500</strong>")
	assert.Contains(t, body, "Set category <strong>Cafe</strong>, Add tag <strong>coffee</strong>")
	rules, err := f.Rules.List(ctx, userID)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, []string{"100", "500"}, rules[0].Conditions[1].Values)
	ruleID := strconv.FormatInt(rules[0].ID, 10)

	status, body = b.get("/rules/" + ruleID)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="100 | 500"`)

	status, body = b.post("/rules/"+ruleID+"/apply", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Changed 1 operation.")
	got, err := f.Operations.Get(ctx, userID, op.ID)
	require.NoError(t, err)
	assert.Equal(t, cafe.ID, got.CategoryID)
	assert.Equal(t, []string{"coffee"}, got.Tags)

	// the payee fills an empty category on the new operation form
	status, body = b.post("/operations/fill", url.Values{
		"type": {"expense"}, "account": {accID}, "payee": {"coffee house"}, "amount": {"200"}, "date": {"2026-10-30"},
	})
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, `value="`+cafeID+`" selected`)
	assert.Contains(t, body, `value="coffee"`)

	status, body = b.post("/operations/rules?account="+accID, nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "Rules changed 0 operations.")

	status, body = b.post("/settings", url.Values{"timezone": {"Europe/Moscow"}})
	require.Equal(t, http.StatusOK, status, body)
	assert.NotContains(t, body, `name="auto_category_rules" checked`)

	status, body = b.post("/rules/"+ruleID+"/delete", nil)
	require.Equal(t, http.StatusOK, status, body)
	assert.Contains(t, body, "No rules yet")
}
