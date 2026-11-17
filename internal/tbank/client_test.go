package tbank_test

import (
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/tbank"
)

func newClient(t *testing.T, h http.HandlerFunc) *tbank.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return tbank.New(srv.Client(), srv.URL, srv.URL, slog.New(slog.DiscardHandler))
}

func write(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	_, err := w.Write([]byte(body))
	assert.NoError(t, err)
}

func TestLogin(t *testing.T) {
	t.Parallel()

	var challenge string
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())
		if r.URL.Path != "/auth/authorize" {
			// the conversation cookie of authorize goes with every step
			if c, err := r.Cookie("SSO_CONVERSATION"); r.URL.Path == "/auth/step" && assert.NoError(t, err) {
				assert.Equal(t, "conv", c.Value)
			}
		}
		switch r.URL.Path + " " + r.PostFormValue("step") + r.PostFormValue("grant_type") {
		case "/auth/authorize ":
			q := r.URL.Query()
			assert.Equal(t, "gorod-app", q.Get("client_id"))
			assert.Equal(t, "S256", q.Get("code_challenge_method"))
			assert.Equal(t, "DEVICE", q.Get("device_id"))
			challenge = q.Get("code_challenge")
			http.SetCookie(w, &http.Cookie{Name: "SSO_CONVERSATION", Value: "conv", Path: "/"})
			write(t, w, `{"cid": "CID", "step": "entry"}`)
		case "/auth/step phone":
			assert.Equal(t, "CID", r.URL.Query().Get("cid"))
			assert.Equal(t, "+79990000000", r.PostFormValue("phone"))
			write(t, w, `{"step": "otp", "token": "t1"}`)
		case "/auth/step otp":
			assert.Equal(t, "1234", r.PostFormValue("otp"))
			assert.Equal(t, "t1", r.PostFormValue("token"), "the token of a step goes to the next")
			write(t, w, `{"step": "password", "token": "t2"}`)
		case "/auth/step password":
			assert.Equal(t, "secret", r.PostFormValue("password"))
			assert.Equal(t, "t2", r.PostFormValue("token"))
			write(t, w, `{"step": "complete", "token": "t3"}`)
		case "/auth/step complete":
			assert.Empty(t, r.PostFormValue("token"), "the complete hop goes bare")
			write(t, w, `{"code": "CODE"}`)
		case "/auth/token/mobile authorization_code":
			user, pass, ok := r.BasicAuth()
			assert.True(t, ok)
			assert.Equal(t, "gorod-app", user)
			assert.Empty(t, pass)
			assert.Equal(t, "CODE", r.PostFormValue("code"))
			sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
			assert.Equal(t, challenge, base64.RawURLEncoding.EncodeToString(sum[:]))
			write(t, w, `{"access_token": "AT", "refresh_token": "RT", "expires_in": 7199,
				"mobile": {"sessionid": "SID"}}`)
		default:
			t.Errorf("unexpected %s %s", r.URL.Path, r.PostForm)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	l, err := c.StartLogin(t.Context(), "DEVICE", "+79990000000")
	require.NoError(t, err)
	assert.Equal(t, tbank.StepOTP, l.Step)

	_, done, err := c.Confirm(t.Context(), l, "1234")
	require.NoError(t, err)
	assert.False(t, done)
	assert.Equal(t, tbank.StepPassword, l.Step)

	s, done, err := c.Confirm(t.Context(), l, "secret")
	require.NoError(t, err)
	assert.True(t, done)
	assert.Equal(t, "DEVICE", s.DeviceID)
	assert.Equal(t, "AT", s.AccessToken)
	assert.Equal(t, "RT", s.RefreshToken)
	assert.Equal(t, "SID", s.SessionID)
	assert.WithinDuration(t, time.Now().Add(7199*time.Second), s.ExpiresAt, time.Minute)
}

func TestLoginStepError(t *testing.T) {
	t.Parallel()

	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/authorize" {
			write(t, w, `{"cid": "CID"}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		write(t, w, `{"error": "invalid_request", "error_description": "phone +79990000000"}`)
	})
	_, err := c.StartLogin(t.Context(), "", "+79990000000")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid_request")
	assert.NotContains(t, err.Error(), "+7999", "answers are not put in errors")
}

func TestRefresh(t *testing.T) {
	t.Parallel()

	answer := `{"access_token": "AT2", "refresh_token": "RT2", "expires_in": 7199, "mobile": {"sessionid": "SID2"}}`
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, r.ParseForm())
		assert.Equal(t, "/auth/token/mobile", r.URL.Path)
		assert.Equal(t, "refresh_token", r.PostFormValue("grant_type"))
		assert.Equal(t, "DEVICE", r.PostFormValue("device_id"))
		if r.PostFormValue("refresh_token") == "dead" {
			w.WriteHeader(http.StatusBadRequest)
			write(t, w, `{"error": "invalid_grant"}`)
			return
		}
		write(t, w, answer)
	})
	old := tbank.Session{DeviceID: "DEVICE", AccessToken: "AT", RefreshToken: "RT", SessionID: "SID"}

	s, err := c.Refresh(t.Context(), old)
	require.NoError(t, err)
	assert.Equal(t, tbank.Session{
		DeviceID: "DEVICE", AccessToken: "AT2", RefreshToken: "RT2", SessionID: "SID2",
		ExpiresAt: s.ExpiresAt,
	}, s, "the refresh token rotates")

	answer = `{"access_token": "AT3", "expires_in": 7199, "mobile": {"sessionid": "SID3"}}`
	s, err = c.Refresh(t.Context(), old)
	require.NoError(t, err)
	assert.Equal(t, "RT", s.RefreshToken, "kept when the bank does not rotate it")

	old.RefreshToken = "dead"
	_, err = c.Refresh(t.Context(), old)
	assert.ErrorIs(t, err, tbank.ErrSessionExpired)
}

func TestOperations(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	status, answer := http.StatusOK, ""
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "/v1/operations", r.URL.Path)
		assert.Equal(t, "Bearer AT", r.Header.Get("Authorization"))
		assert.Equal(t, "SID", q.Get("sessionid"))
		assert.Equal(t, "DEVICE", q.Get("deviceId"))
		assert.Equal(t, "1790812800000", q.Get("start"))
		assert.Equal(t, "1793491200000", q.Get("end"))
		w.WriteHeader(status)
		write(t, w, answer)
	})
	s := tbank.Session{DeviceID: "DEVICE", AccessToken: "AT", SessionID: "SID"}

	answer = `{"resultCode": "OK", "payload": [{"id": "1", "account": "1001", "type": "Debit", "status": "OK",
		"operationTime": {"milliseconds": 1793836800123}, "accountAmount": {"value": 10.5},
		"operationId": {"source": "PrimeAuth"}}]}`
	ops, err := c.Operations(t.Context(), s, from, to)
	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "-10.5", ops[0].Amount().String())
	assert.True(t, ops[0].Pending(), "an authorization without a debiting time")
	assert.Equal(t, int64(1793836800123), ops[0].Time().UnixMilli())

	answer = `{"resultCode": "INSUFFICIENT_PRIVILEGES"}`
	_, err = c.Operations(t.Context(), s, from, to)
	require.ErrorIs(t, err, tbank.ErrSessionExpired)

	status, answer = http.StatusUnauthorized, ""
	_, err = c.Operations(t.Context(), s, from, to)
	require.ErrorIs(t, err, tbank.ErrSessionExpired)

	status, answer = http.StatusOK, `{"resultCode": "INTERNAL_ERROR"}`
	_, err = c.Operations(t.Context(), s, from, to)
	require.Error(t, err)
	assert.False(t, errors.Is(err, tbank.ErrSessionExpired))
	assert.NotContains(t, err.Error(), "SID", "the session is not put in errors")
}
