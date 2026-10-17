package netx

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

const (
	// Data transport buffer size. Go's http.Transport defaults its per-connection
	// bufio reader/writer to 4 KiB, so a larger application buffer still ends up
	// issuing syscalls in 4 KiB steps. Raise both to match the engine's read buffer.
	dataBufSize = 128 * 1024
)

// NewDataClient returns an HTTP/1.1-forced client for multi-connection downloads.
// Each connection gets its own CWND; H2 multiplexing would defeat parallel Range.
func NewDataClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    true,
		TLSNextProto:          map[string]func(authority string, c *tls.Conn) http.RoundTripper{},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: timeout,
		// Do not set SO_RCVBUF/SO_SNDBUF: pinning the socket buffer disables
		// kernel receive-window autotuning on both Windows and Linux.
		ReadBufferSize:  dataBufSize,
		WriteBufferSize: dataBufSize,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   0, // per-request context controls lifetime
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

// NewProbeClient allows HTTP/2 for probe/grab (single request, multiplexing OK).
func NewProbeClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        32,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
			DisableCompression:  false,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}
