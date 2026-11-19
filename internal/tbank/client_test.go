package tbank_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/tbank"
)

// session is the session the client stores.
type session struct {
	DeviceID     string    `json:"device_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	SessionID    string    `json:"session_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func encode(t *testing.T, s session) []byte {
	t.Helper()
	b, err := json.Marshal(s)
	require.NoError(t, err)
	return b
}

func decode(t *testing.T, b []byte) session {
	t.Helper()
	var s session
	require.NoError(t, json.Unmarshal(b, &s))
	return s
}

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

	l, err := c.StartLogin(t.Context(), encode(t, session{DeviceID: "DEVICE"}), "+79990000000")
	require.NoError(t, err)
	assert.Equal(t, domain.BankStepOTP, l.Step())

	_, done, err := c.Confirm(t.Context(), l, "1234")
	require.NoError(t, err)
	assert.False(t, done)
	assert.Equal(t, domain.BankStepPassword, l.Step())

	b, done, err := c.Confirm(t.Context(), l, "secret")
	require.NoError(t, err)
	assert.True(t, done)
	s := decode(t, b)
	assert.Equal(t, "DEVICE", s.DeviceID, "the device of the last session")
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
	_, err := c.StartLogin(t.Context(), nil, "+79990000000")
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
	old := session{DeviceID: "DEVICE", AccessToken: "AT", RefreshToken: "RT", SessionID: "SID"}

	b, err := c.Refresh(t.Context(), encode(t, old))
	require.NoError(t, err)
	s := decode(t, b)
	assert.Equal(t, session{
		DeviceID: "DEVICE", AccessToken: "AT2", RefreshToken: "RT2", SessionID: "SID2",
		ExpiresAt: s.ExpiresAt,
	}, s, "the refresh token rotates")

	answer = `{"access_token": "AT3", "expires_in": 7199, "mobile": {"sessionid": "SID3"}}`
	b, err = c.Refresh(t.Context(), encode(t, old))
	require.NoError(t, err)
	assert.Equal(t, "RT", decode(t, b).RefreshToken, "kept when the bank does not rotate it")

	old.RefreshToken = "dead"
	_, err = c.Refresh(t.Context(), encode(t, old))
	assert.ErrorIs(t, err, domain.ErrBankLoginRequired)
}

func TestFetchErrors(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	status, answer := http.StatusOK, ""
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "Bearer AT", r.Header.Get("Authorization"))
		assert.Equal(t, "SID", q.Get("sessionid"))
		assert.Equal(t, "DEVICE", q.Get("deviceId"))
		if r.URL.Path == "/v1/accounts_light" {
			write(t, w, `{"resultCode": "OK", "payload": []}`)
			return
		}
		assert.Equal(t, "/v1/operations", r.URL.Path)
		assert.Equal(t, "1790812800000", q.Get("start"))
		assert.Equal(t, "1793491200000", q.Get("end"))
		w.WriteHeader(status)
		write(t, w, answer)
	})
	s := encode(t, session{DeviceID: "DEVICE", AccessToken: "AT", SessionID: "SID"})

	answer = `{"resultCode": "OK", "payload": []}`
	_, err := c.Fetch(t.Context(), s, from, to, time.UTC)
	require.NoError(t, err)

	answer = `{"resultCode": "INSUFFICIENT_PRIVILEGES"}`
	_, err = c.Fetch(t.Context(), s, from, to, time.UTC)
	require.ErrorIs(t, err, domain.ErrBankLoginRequired)

	status, answer = http.StatusUnauthorized, ""
	_, err = c.Fetch(t.Context(), s, from, to, time.UTC)
	require.ErrorIs(t, err, domain.ErrBankLoginRequired)

	status, answer = http.StatusOK, `{"resultCode": "INTERNAL_ERROR"}`
	_, err = c.Fetch(t.Context(), s, from, to, time.UTC)
	require.Error(t, err)
	assert.False(t, errors.Is(err, domain.ErrBankLoginRequired))
	assert.NotContains(t, err.Error(), "SID", "the session is not put in errors")
}

func TestFetchWindows(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	var (
		mu     sync.Mutex
		starts []string
		failed bool
	)
	c := newClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/accounts_light" {
			write(t, w, `{"resultCode": "OK", "payload": []}`)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if !failed {
			failed = true
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		starts = append(starts, r.URL.Query().Get("start"))
		write(t, w, `{"resultCode": "OK", "payload": []}`)
	})
	s := encode(t, session{DeviceID: "DEVICE", AccessToken: "AT", SessionID: "SID"})

	_, err := c.Fetch(t.Context(), s, from, to, time.UTC)
	require.NoError(t, err, "a 503 is tried again")
	ms := func(t time.Time) string { return strconv.FormatInt(t.UnixMilli(), 10) }
	assert.Equal(t, []string{
		ms(from),
		ms(from.Add(31*24*time.Hour + time.Millisecond)),
		ms(from.Add(62*24*time.Hour + 2*time.Millisecond)),
	}, starts, "31-day windows")
}
