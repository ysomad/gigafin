// Package httpclient creates the HTTP clients of outgoing requests, so every one has
// timeouts and its own connection pool.
package httpclient

import (
	"net"
	"net/http"
	"runtime"
	"time"
)

// New returns a client whose requests, reading the body included, time out after
// timeout. Its transport has the settings of the pooled transport of
// github.com/hashicorp/go-cleanhttp: a transport of its own instead of the shared
// http.DefaultTransport, bounded dialing and TLS handshakes, and idle connections kept
// per host for reuse.
func New(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConnsPerHost:   runtime.GOMAXPROCS(0) + 1,
		},
	}
}
