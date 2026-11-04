package exchangerate_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/exchangerate"
)

func cbrServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request) bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if handler != nil && handler(w, r) {
			return
		}
		var file string
		switch {
		case r.URL.Path == "/XML_valFull.asp":
			file = "testdata/valfull.xml"
		case r.URL.Path == "/XML_dynamic.asp" && r.URL.Query().Get("VAL_NM_RQ") == "R01235":
			file = "testdata/dynamic_usd.xml"
		case r.URL.Path == "/XML_dynamic.asp" && r.URL.Query().Get("VAL_NM_RQ") == "R01030":
			file = "testdata/dynamic_dzd.xml"
		default:
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile(file)
		if !assert.NoError(t, err) {
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=windows-1251")
		_, err = w.Write(b)
		assert.NoError(t, err)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCBRRates(t *testing.T) {
	t.Parallel()

	var valFull atomic.Int32
	srv := cbrServer(t, func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/XML_valFull.asp" {
			valFull.Add(1)
		}
		assert.NotEmpty(t, r.UserAgent())
		return false
	})
	c := exchangerate.NewCBR(srv.Client(), srv.URL, 0)

	rates, err := c.Rates(t.Context(), "USD", day("2026-10-01"), day("2026-10-06"))
	require.NoError(t, err)
	require.Len(t, rates, 4)
	assert.Equal(t, domain.RateSourceCBR, rates[0].Source)
	assert.Equal(t, "USD", rates[0].Currency)
	assert.Equal(t, day("2026-10-01"), rates[0].Date)
	assert.True(t, decimal.RequireFromString("83.5588").Equal(rates[0].Rate))
	assert.Equal(t, day("2026-10-06"), rates[3].Date, "no rates on the weekend")

	// no VunitRate: Value divided by Nominal
	rates, err = c.Rates(t.Context(), "DZD", day("2026-10-01"), day("2026-10-02"))
	require.NoError(t, err)
	require.Len(t, rates, 1)
	assert.True(t, decimal.RequireFromString("0.621").Equal(rates[0].Rate))

	_, err = c.Rates(t.Context(), "EUR", day("2026-10-01"), day("2026-10-02"))
	require.Error(t, err, "currency missing from the list")
	assert.Equal(t, int32(1), valFull.Load(), "currency list loaded once")
}

func TestCBRRetry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := cbrServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/XML_dynamic.asp" && calls.Add(1) <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	c := exchangerate.NewCBR(srv.Client(), srv.URL, 0)

	rates, err := c.Rates(t.Context(), "USD", day("2026-10-01"), day("2026-10-06"))
	require.NoError(t, err)
	assert.Len(t, rates, 4)
	assert.Equal(t, int32(3), calls.Load())
}

func TestCBRNoRetryOnClientError(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := cbrServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/XML_dynamic.asp" {
			calls.Add(1)
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		return false
	})
	c := exchangerate.NewCBR(srv.Client(), srv.URL, 0)

	_, err := c.Rates(t.Context(), "USD", day("2026-10-01"), day("2026-10-06"))
	require.Error(t, err)
	assert.Equal(t, int32(1), calls.Load())
}

func TestCBRSpacesRequests(t *testing.T) {
	t.Parallel()

	srv := cbrServer(t, nil)
	c := exchangerate.NewCBR(srv.Client(), srv.URL, 100*time.Millisecond)

	start := time.Now()
	for range 2 {
		_, err := c.Rates(t.Context(), "USD", day("2026-10-01"), day("2026-10-06"))
		require.NoError(t, err)
	}
	// currency list and two rate requests: two intervals after the first
	assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond)
}
