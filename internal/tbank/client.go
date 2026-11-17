// Package tbank syncs T-Bank through its private mobile API, the one the iOS app uses:
// sign-in by phone, SMS code and password, and reading accounts and operations. The
// API is undocumented, so the client sends what the app sends and reads only the
// fields gigafin needs. Errors never carry tokens or response bodies.
package tbank

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/money"
)

// Hosts of the API.
const (
	IDURL  = "https://id.t-bank-app.ru"
	APIURL = "https://api.t-bank-app.ru"
)

// What the iOS app sends; the bank may reject an outdated app version.
const (
	_clientID      = "gorod-app"
	_vendor        = "t_ios"
	_clientVersion = "130.0.0"
	_appName       = "mobile"
	_appVersion    = "8.2.9"
	_platform      = "ios"
	_origin        = "mobile,ib5,loyalty,platform"
	_redirectURI   = "mobile://"
	_userAgent     = "iPhone/iOS(18.6.2)/TCSMB/8.2.9(829000)"
	_claims        = `{"id_token":{"given_name":{"essential":true},"phone_number":{"essential":true},` +
		`"picture":{"essential":true},"api_sso_id":{"essential":true}}}`
)

// _stepComplete is the step the bank asks for once every factor is accepted; it
// needs no input. The steps that do are named as domain.BankStepOTP and
// domain.BankStepPassword.
const _stepComplete = "complete"

// Client calls the API.
type Client struct {
	http   *http.Client
	idURL  string
	apiURL string
	log    *slog.Logger
}

// New returns a client of the API at idURL and apiURL over hc, which must trust the
// Russian Trusted Root CA. Requests are logged to log at debug level, without their query, which
// holds the session, or any body.
func New(hc *http.Client, idURL, apiURL string, log *slog.Logger) *Client {
	return &Client{http: hc, idURL: idURL, apiURL: apiURL, log: log}
}

// session is a signed-in device, stored by gigafin as JSON. RefreshToken rotates on
// every refresh, so the session returned by Refresh must replace the old one.
type session struct {
	DeviceID     string    `json:"device_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	SessionID    string    `json:"session_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// login is a sign-in in progress.
type login struct {
	deviceID string
	step     string
	cid      string
	token    string
	verifier string
	jar      http.CookieJar
}

// Step returns the input the bank asks for next.
func (l *login) Step() string {
	return l.step
}

// account is a bank account. Amount fields are zero when the bank omits them.
type account struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AccountType string `json:"accountType"`
	Hidden      bool   `json:"hidden"`
	Currency    struct {
		Name string `json:"name"`
	} `json:"currency"`
	MoneyAmount amount `json:"moneyAmount"`
	CreditLimit amount `json:"creditLimit"`
	DebtAmount  amount `json:"debtAmount"`
}

// Kinds of accounts other than current ones.
const (
	_accountSaving   = "Saving"
	_accountCredit   = "Credit"
	_accountCashLoan = "CashLoan"
)

// tracked reports whether the account can sync with a gigafin account: it is not
// hidden and has a currency, unlike an installment plan.
func (a account) tracked() bool {
	return !a.Hidden && strings.TrimSpace(a.Name) != "" && money.ValidCurrency(strings.ToUpper(a.Currency.Name))
}

// balance returns the signed balance of the account as gigafin counts it: what is on
// a current or savings account, own money minus the limit on a credit card, and the
// negative debt of a loan.
func (a account) balance() money.Amount {
	switch a.AccountType {
	case _accountCredit:
		return a.MoneyAmount.Value.Sub(a.CreditLimit.Value)
	case _accountCashLoan:
		return a.DebtAmount.Value
	default:
		return a.MoneyAmount.Value
	}
}

// operation is a bank operation of any account.
type operation struct {
	ID          string `json:"id"`
	Account     string `json:"account"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	Description string `json:"description"`
	Message     string `json:"message"`
	MCC         string `json:"mccString"`
	OperationID struct {
		Source string `json:"source"`
	} `json:"operationId"`
	OperationTime    millis  `json:"operationTime"`
	DebitingTime     *millis `json:"debitingTime"`
	AccountAmount    amount  `json:"accountAmount"`
	SpendingCategory struct {
		Name string `json:"name"`
	} `json:"spendingCategory"`
	Payment struct {
		Comment string `json:"comment"`
	} `json:"payment"`
}

// Operation fields gigafin checks.
const (
	_opStatusOK = "OK"
	_opDebit    = "Debit"
	// _opSourceAuth is an authorization; without DebitingTime it is a hold that may
	// still change or be canceled.
	_opSourceAuth = "PrimeAuth"
)

// value returns the amount of the operation in the account currency, negative for
// money out.
func (o operation) value() money.Amount {
	if o.Type == _opDebit {
		return o.AccountAmount.Value.Neg()
	}
	return o.AccountAmount.Value
}

// at returns when the operation happened.
func (o operation) at() time.Time {
	return time.UnixMilli(o.OperationTime.Milliseconds)
}

// pending reports whether the operation is a hold not yet debited.
func (o operation) pending() bool {
	return o.OperationID.Source == _opSourceAuth && o.DebitingTime == nil
}

type amount struct {
	Value    money.Amount `json:"value"`
	Currency struct {
		Name string `json:"name"`
	} `json:"currency"`
}

type millis struct {
	Milliseconds int64 `json:"milliseconds"`
}

// StartLogin starts a sign-in by phone in the international form, on the device of
// prev, the session of the last sign-in, so the bank does not see a new one; a device
// is made up without it. The bank sends an SMS code.
//
//nolint:ireturn // the sign-in is kept by the caller as domain.BankLogin
func (c *Client) StartLogin(ctx context.Context, prev []byte, phone string) (domain.BankLogin, error) {
	var old session
	if prev != nil {
		if err := json.Unmarshal(prev, &old); err != nil {
			return nil, fmt.Errorf("tbank: session: %w", err)
		}
	}
	deviceID := cmp.Or(old.DeviceID, strings.ToUpper(randomHex(16)))
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("tbank: cookie jar: %w", err)
	}
	l := &login{deviceID: deviceID, verifier: randomHex(32), jar: jar}
	challenge := sha256.Sum256([]byte(l.verifier))
	q := url.Values{
		"claims":                {_claims},
		"client_id":             {_clientID},
		"client_version":        {_clientVersion},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
		"device_id":             {deviceID},
		"display":               {"json"},
		"redirect_uri":          {_redirectURI},
		"response_mode":         {"json"},
		"response_type":         {"code"},
		"state":                 {randomHex(16)},
		"vendor":                {_vendor},
		"ccc":                   {"true"},
		"cpswc":                 {"true"},
	}
	var auth struct {
		CID string `json:"cid"`
	}
	if err := c.do(ctx, l.jar, http.MethodGet, c.idURL+"/auth/authorize?"+q.Encode(), nil, false, &auth); err != nil {
		return nil, fmt.Errorf("tbank: authorize: %w", err)
	}
	if auth.CID == "" {
		return nil, errors.Error("tbank: authorize: no cid")
	}
	l.cid = auth.CID
	if _, err := c.step(ctx, l, url.Values{"step": {"phone"}, "phone": {phone}, "fingerprint": {""}}); err != nil {
		return nil, err
	}
	return l, nil
}

// Confirm answers the step the login asks for with value. done is false when the
// bank asks for another step, which l.Step names; otherwise it returns the session
// signed in.
func (c *Client) Confirm(ctx context.Context, bl domain.BankLogin, value string) ([]byte, bool, error) {
	l, ok := bl.(*login)
	if !ok {
		return nil, false, errors.Error("tbank: not a t-bank sign-in")
	}
	code, err := c.step(ctx, l, url.Values{"step": {l.step}, l.step: {value}})
	if err != nil {
		return nil, false, err
	}
	if code == "" && l.step == _stepComplete {
		// every factor is accepted; one more hop without input returns the code
		if code, err = c.step(ctx, l, url.Values{"step": {_stepComplete}}); err != nil {
			return nil, false, err
		}
	}
	if code == "" {
		return nil, false, nil
	}
	s, err := c.token(ctx, l.deviceID, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"code_verifier": {l.verifier},
	})
	if err != nil {
		return nil, false, err
	}
	b, err := json.Marshal(s) //nolint:gosec // G117: the caller encrypts it
	if err != nil {
		return nil, false, fmt.Errorf("tbank: session: %w", err)
	}
	return b, true, nil
}

// Refresh exchanges the refresh token of the session for a new session; the old
// refresh token stops working. It fails with domain.ErrBankLoginRequired when the
// bank rejects it.
func (c *Client) Refresh(ctx context.Context, sess []byte) ([]byte, error) {
	var s session
	if err := json.Unmarshal(sess, &s); err != nil {
		return nil, fmt.Errorf("tbank: session: %w", err)
	}
	fresh, err := c.token(ctx, s.DeviceID, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {s.RefreshToken},
	})
	if err != nil {
		return nil, err
	}
	fresh.RefreshToken = cmp.Or(fresh.RefreshToken, s.RefreshToken)
	b, err := json.Marshal(fresh) //nolint:gosec // G117: the caller encrypts it
	if err != nil {
		return nil, fmt.Errorf("tbank: session: %w", err)
	}
	return b, nil
}

// Fetch returns the tracked accounts and the operations between from and to that
// import, with times in loc.
func (c *Client) Fetch(
	ctx context.Context, sess []byte, from, to time.Time, loc *time.Location,
) (domain.BankData, error) {
	var s session
	if err := json.Unmarshal(sess, &s); err != nil {
		return domain.BankData{}, fmt.Errorf("tbank: session: %w", err)
	}
	var accs []account
	if err := c.get(ctx, s, "/v1/accounts_light", url.Values{}, &accs); err != nil {
		return domain.BankData{}, fmt.Errorf("tbank: accounts: %w", err)
	}
	q := url.Values{
		"start": {strconv.FormatInt(from.UnixMilli(), 10)},
		"end":   {strconv.FormatInt(to.UnixMilli(), 10)},
	}
	var ops []operation
	if err := c.get(ctx, s, "/v1/operations", q, &ops); err != nil {
		return domain.BankData{}, fmt.Errorf("tbank: operations: %w", err)
	}
	return bankData(accs, ops, loc), nil
}

// step posts a sign-in step and moves l to the step the bank asks for next. It
// returns the authorization code once the bank gives one.
func (c *Client) step(ctx context.Context, l *login, form url.Values) (string, error) {
	if l.token != "" && form.Get("step") != _stepComplete {
		form.Set("token", l.token)
	}
	u := c.idURL + "/auth/step?" + url.Values{"cid": {l.cid}, "ccc": {"true"}, "cpswc": {"true"}}.Encode()
	var res struct {
		Step  string `json:"step"`
		Token string `json:"token"`
		Code  string `json:"code"`
	}
	if err := c.do(ctx, l.jar, http.MethodPost, u, form, false, &res); err != nil {
		return "", fmt.Errorf("tbank: step %s: %w", form.Get("step"), err)
	}
	if res.Token != "" {
		l.token = res.Token
	}
	if res.Step != "" {
		l.step = res.Step
	}
	c.log.DebugContext(ctx, "tbank login step", slog.String("sent", form.Get("step")), slog.String("next", res.Step),
		slog.Bool("code", res.Code != ""))
	return res.Code, nil
}

// token calls the token endpoint with a grant and returns the session it issues.
func (c *Client) token(ctx context.Context, deviceID string, grant url.Values) (session, error) {
	grant.Set("device_id", deviceID)
	grant.Set("vendor", _vendor)
	grant.Set("client_version", _clientVersion)
	grant.Set("appName", _appName)
	grant.Set("appVersion", _appVersion)
	grant.Set("platform", _platform)
	grant.Set("origin", _origin)
	grant.Set("redirect_uri", _redirectURI)
	var res struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Mobile       struct {
			SessionID string `json:"sessionid"`
		} `json:"mobile"`
	}
	u := c.idURL + "/auth/token/mobile?ccc=true&cpswc=true"
	if err := c.do(ctx, nil, http.MethodPost, u, grant, true, &res); err != nil {
		return session{}, fmt.Errorf("tbank: token: %w", err)
	}
	if res.AccessToken == "" || res.Mobile.SessionID == "" {
		return session{}, errors.Error("tbank: token: no access token or session")
	}
	return session{
		DeviceID:     deviceID,
		AccessToken:  res.AccessToken,
		RefreshToken: res.RefreshToken,
		SessionID:    res.Mobile.SessionID,
		ExpiresAt:    time.Now().Add(time.Duration(res.ExpiresIn) * time.Second),
	}, nil
}

// get calls a GET method of the API at path with query q and the parameters of the
// app, and decodes the payload of its answer into out.
func (c *Client) get(ctx context.Context, s session, path string, q url.Values, out any) error {
	q.Set("sessionid", s.SessionID)
	q.Set("deviceId", s.DeviceID)
	q.Set("oldDeviceId", s.DeviceID)
	q.Set("appName", _appName)
	q.Set("appVersion", _appVersion)
	q.Set("platform", _platform)
	q.Set("origin", _origin)
	q.Set("connectionType", "WiFi")
	q.Set("ccc", "true")
	q.Set("cpswc", "true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", _userAgent)
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		err = redactURL(err)
		c.log.DebugContext(ctx, "tbank request failed", slog.String("method", http.MethodGet),
			slog.String("path", path), slog.Duration("took", time.Since(start)), slog.Any("error", err))
		return err
	}
	defer resp.Body.Close()
	var env struct {
		ResultCode string          `json:"resultCode"`
		Payload    json.RawMessage `json:"payload"`
	}
	var decodeErr error
	if resp.StatusCode == http.StatusOK {
		decodeErr = json.NewDecoder(resp.Body).Decode(&env)
	}
	c.log.DebugContext(ctx, "tbank request", slog.String("method", http.MethodGet), slog.String("path", path),
		slog.Int("status", resp.StatusCode), slog.String("result", env.ResultCode),
		slog.Int("payload_bytes", len(env.Payload)), slog.Duration("took", time.Since(start)))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return domain.ErrBankLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	if decodeErr != nil {
		return fmt.Errorf("decode: %w", decodeErr)
	}
	switch env.ResultCode {
	case "OK":
	case "INSUFFICIENT_PRIVILEGES", "WRONG_SESSION", "SESSION_EXPIRED", "AUTHENTICATION_FAILED":
		return fmt.Errorf("result %s: %w", env.ResultCode, domain.ErrBankLoginRequired)
	default:
		return fmt.Errorf("result %q", env.ResultCode)
	}
	if err := json.Unmarshal(env.Payload, out); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	return nil
}

// do sends a request to the sign-in host, a form when form is not nil, as the app
// client to the token endpoint when client is set, and decodes the JSON answer into
// out. OAuth errors are returned by their code only.
func (c *Client) do(
	ctx context.Context, jar http.CookieJar, method, u string, form url.Values, client bool, out any,
) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", _userAgent)
	if client {
		req.Header.Set("x-sso-no-adapter", "true")
		req.SetBasicAuth(_clientID, "")
	}
	hc := *c.http
	hc.Jar = jar
	start := time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		err = redactURL(err)
		c.log.DebugContext(ctx, "tbank request failed", slog.String("method", method), slog.String("path", req.URL.Path),
			slog.Duration("took", time.Since(start)), slog.Any("error", err))
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	var oauth struct {
		Error string `json:"error"`
	}
	// the error is checked below; the code goes to the log first
	oauthErr := json.Unmarshal(b, &oauth)
	c.log.DebugContext(ctx, "tbank request", slog.String("method", method), slog.String("path", req.URL.Path),
		slog.String("step", form.Get("step")), slog.String("grant", form.Get("grant_type")),
		slog.Int("status", resp.StatusCode), slog.String("error", oauth.Error), slog.Duration("took", time.Since(start)))
	// error answers may not be JSON, the status tells them apart then
	if oauthErr != nil && resp.StatusCode == http.StatusOK {
		return fmt.Errorf("decode: %w", oauthErr)
	}
	switch {
	case oauth.Error == "invalid_grant":
		return domain.ErrBankLoginRequired
	case oauth.Error != "":
		return fmt.Errorf("%s (http status %d)", oauth.Error, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// redactURL drops the URL from a transport error, as its query holds the session.
func redactURL(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
