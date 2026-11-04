// Package exchangerate fetches exchange rates from their sources, one file per source.
package exchangerate

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/time/rate"

	"github.com/ysomad/gigafin/internal/domain"
)

// CBRURL is the base URL of the CBR XML API, see https://www.cbr.ru/development/sxml/.
const CBRURL = "https://www.cbr.ru/scripts"

const (
	_cbrDateLayout = "02/01/2006"
	_cbrDayLayout  = "02.01.2006"
	// the site is behind ddos-guard, which turns away clients that do not look like a
	// browser
	_cbrUserAgent = "Mozilla/5.0 (compatible; gigafin)"
	_cbrMaxBody   = 16 << 20
	// _cbrAttempts bounds tries of one request answered with 429 or 5xx
	_cbrAttempts = 5
	// _cbrMaxWait bounds a wait between tries, whatever Retry-After asks for
	_cbrMaxWait = time.Minute
)

// CBR fetches daily rates of the Central Bank of Russia in rubles. It is safe for
// concurrent use; requests are spaced by the interval given to NewCBR and retried with
// backoff when the server limits them.
type CBR struct {
	client  *http.Client
	baseURL string
	limiter *rate.Limiter

	mu sync.Mutex
	// codes maps an ISO code to the CBR code of its rate history, loaded once
	codes map[string]string
}

// NewCBR returns a client of the API at baseURL that sends at most one request per
// every; zero does not space them.
func NewCBR(client *http.Client, baseURL string, every time.Duration) *CBR {
	limit := rate.Inf
	if every > 0 {
		limit = rate.Every(every)
	}
	return &CBR{client: client, baseURL: strings.TrimSuffix(baseURL, "/"), limiter: rate.NewLimiter(limit, 1)}
}

// Rates returns the rates of currency set in the range from to to, both dates included. The CBR
// sets no rate on weekends and holidays, so some days have none.
func (c *CBR) Rates(ctx context.Context, currency string, from, to time.Time) ([]domain.Rate, error) {
	code, err := c.code(ctx, currency)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Records []struct {
			Date      string `xml:"Date,attr"`
			Nominal   string `xml:"Nominal"`
			Value     string `xml:"Value"`
			VunitRate string `xml:"VunitRate"`
		} `xml:"Record"`
	}
	q := url.Values{
		"date_req1": {from.Format(_cbrDateLayout)},
		"date_req2": {to.Format(_cbrDateLayout)},
		"VAL_NM_RQ": {code},
	}
	if err := c.get(ctx, "XML_dynamic.asp", q, &resp); err != nil {
		return nil, err
	}
	rates := make([]domain.Rate, len(resp.Records))
	for i, rec := range resp.Records {
		date, err := time.Parse(_cbrDayLayout, rec.Date)
		if err != nil {
			return nil, fmt.Errorf("cbr %s: date %q: %w", currency, rec.Date, err)
		}
		// VunitRate is Value divided by Nominal, missing in old answers
		v, err := decimal.NewFromString(strings.ReplaceAll(strings.TrimSpace(rec.VunitRate), ",", "."))
		if rec.VunitRate == "" {
			v, err = cbrUnitRate(rec.Value, rec.Nominal)
		}
		if err != nil || !v.IsPositive() {
			return nil, fmt.Errorf("cbr %s %s: rate %q: bad value", currency, rec.Date, rec.VunitRate)
		}
		rates[i] = domain.Rate{Source: domain.RateSourceCBR, Currency: currency, Date: date, Rate: v}
	}
	return rates, nil
}

// code returns the CBR code of currency, loading the currency list on first use.
func (c *CBR) code(ctx context.Context, currency string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.codes == nil {
		var resp struct {
			Items []struct {
				ID         string `xml:"ID,attr"`
				ParentCode string `xml:"ParentCode"`
				ISO        string `xml:"ISO_Char_Code"`
			} `xml:"Item"`
		}
		if err := c.get(ctx, "XML_valFull.asp", nil, &resp); err != nil {
			return "", err
		}
		codes := make(map[string]string, len(resp.Items))
		for _, it := range resp.Items {
			iso := strings.TrimSpace(it.ISO)
			if _, ok := codes[iso]; ok || iso == "" {
				continue
			}
			// the parent code keeps the history across nominal changes, such as R01020A
			// under R01020
			code := strings.TrimSpace(it.ParentCode)
			if code == "" {
				code = strings.TrimSpace(it.ID)
			}
			codes[iso] = code
		}
		c.codes = codes
	}
	code, ok := c.codes[currency]
	if !ok {
		return "", fmt.Errorf("cbr: no rates of %s", currency)
	}
	return code, nil
}

// get decodes the XML answer of path to v. It waits for the limiter before every try
// and retries answers of 429 and 5xx and network failures, waiting as long as the
// Retry-After header asks or backing off exponentially.
func (c *CBR) get(ctx context.Context, path string, q url.Values, v any) error {
	u := c.baseURL + "/" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var lastErr error
	for attempt := range _cbrAttempts {
		if err := c.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("cbr %s: %w", path, err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("cbr %s: %w", path, err)
		}
		req.Header.Set("User-Agent", _cbrUserAgent)

		wait := min(time.Second<<attempt, _cbrMaxWait)
		resp, err := c.client.Do(req)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return fmt.Errorf("cbr %s: %w", path, ctx.Err())
			}
			lastErr = fmt.Errorf("cbr %s: %w", path, err)
		case resp.StatusCode == http.StatusOK:
			dec := xml.NewDecoder(io.LimitReader(resp.Body, _cbrMaxBody))
			dec.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
				if !strings.EqualFold(label, "windows-1251") {
					return nil, fmt.Errorf("unsupported charset %q", label)
				}
				return charmap.Windows1251.NewDecoder().Reader(r), nil
			}
			err := dec.Decode(v)
			_ = resp.Body.Close()
			if err != nil {
				return fmt.Errorf("cbr %s: decode: %w", path, err)
			}
			return nil
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError:
			if after, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
				wait = min(after, _cbrMaxWait)
			}
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("cbr %s: status %s", path, resp.Status)
		default:
			_ = resp.Body.Close()
			return fmt.Errorf("cbr %s: status %s", path, resp.Status)
		}
		if attempt == _cbrAttempts-1 {
			break
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return fmt.Errorf("cbr %s: %w", path, ctx.Err())
		case <-t.C:
		}
	}
	return lastErr
}

// retryAfter parses a Retry-After header of seconds or an HTTP date relative to now.
func retryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if s, err := strconv.Atoi(h); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}
	if t, err := http.ParseTime(h); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

// cbrUnitRate returns value divided by nominal, both with a decimal comma.
func cbrUnitRate(value, nominal string) (decimal.Decimal, error) {
	v, err := decimal.NewFromString(strings.ReplaceAll(strings.TrimSpace(value), ",", "."))
	if err != nil {
		return decimal.Decimal{}, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(nominal), 10, 64)
	if err != nil || n <= 0 {
		return decimal.Decimal{}, fmt.Errorf("nominal %q: bad value", nominal)
	}
	return v.DivRound(decimal.NewFromInt(n), 10), nil
}
