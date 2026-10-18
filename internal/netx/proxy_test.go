package netx

import (
	"net/http"
	"testing"
)

func TestNormalizeProxy(t *testing.T) {
	if got, err := NormalizeProxy("  "); err != nil || got != "" {
		t.Fatalf("empty: %q %v", got, err)
	}
	if got, err := NormalizeProxy("NONE"); err != nil || got != ProxyDirect {
		t.Fatalf("direct: %q %v", got, err)
	}
	got, err := NormalizeProxy("http://127.0.0.1:8080")
	if err != nil || got != "http://127.0.0.1:8080" {
		t.Fatalf("http: %q %v", got, err)
	}
	if _, err := NormalizeProxy("ftp://127.0.0.1:21"); err == nil {
		t.Fatal("ftp proxy should be rejected")
	}
}

func TestWithProxyDirectIgnoresEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	base := NewProbeClient(0)
	client, err := WithProxy(base, "direct")
	if err != nil {
		t.Fatal(err)
	}
	tr := client.Transport.(*http.Transport)
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/a", nil)
	if tr.Proxy != nil {
		if u, err := tr.Proxy(req); err != nil || u != nil {
			t.Fatalf("direct proxy = %v %v", u, err)
		}
	}
}

func TestWithProxyHTTP(t *testing.T) {
	base := NewDataClient(0)
	client, err := WithProxy(base, "socks5://127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	tr := client.Transport.(*http.Transport)
	if tr.DialContext == nil {
		t.Fatal("socks dialer missing")
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.com/a", nil)
	if tr.Proxy != nil {
		if u, _ := tr.Proxy(req); u != nil {
			t.Fatalf("socks should not also set an HTTP proxy: %s", u)
		}
	}
}
