package netx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/proxy"
)

// ProxyDirect disables proxying for one task, including environment variables.
const ProxyDirect = "direct"

// NormalizeProxy accepts an empty string (environment), "direct"/"none"/"off",
// or an http, https, socks5, or socks5h URL.
func NormalizeProxy(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if strings.ContainsAny(s, "\r\n\x00") {
		return "", fmt.Errorf("代理地址包含非法字符")
	}
	if len(s) > 512 {
		return "", fmt.Errorf("代理地址过长")
	}
	switch strings.ToLower(s) {
	case "direct", "none", "off":
		return ProxyDirect, nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("代理地址无效，需要 http(s):// 或 socks5://")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("代理只支持 http、https、socks5")
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("代理地址缺少主机")
	}
	return u.String(), nil
}

// WithProxy returns a client that uses raw instead of the base client's proxy.
// An empty raw returns base unchanged.
func WithProxy(base *http.Client, raw string) (*http.Client, error) {
	spec, err := NormalizeProxy(raw)
	if err != nil {
		return nil, err
	}
	if spec == "" || base == nil {
		return base, nil
	}
	tr := cloneTransport(base)
	if spec == ProxyDirect {
		tr.Proxy = nil
	} else {
		u, err := url.Parse(spec)
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https":
			tr.Proxy = http.ProxyURL(u)
		case "socks5", "socks5h":
			d, err := proxy.FromURL(u, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("SOCKS5 代理无效：%w", err)
			}
			tr.Proxy = nil
			tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if cd, ok := d.(proxy.ContextDialer); ok {
					return cd.DialContext(ctx, network, addr)
				}
				return d.Dial(network, addr)
			}
		default:
			return nil, fmt.Errorf("代理只支持 http、https、socks5")
		}
	}
	cp := *base
	cp.Transport = tr
	return &cp, nil
}

// DialFunc returns a dialer for FTP. HTTP proxies cannot carry FTP.
func DialFunc(raw string) (func(network, addr string) (net.Conn, error), error) {
	spec, err := NormalizeProxy(raw)
	if err != nil {
		return nil, err
	}
	if spec == "" || spec == ProxyDirect {
		d := &net.Dialer{}
		return d.Dial, nil
	}
	u, err := url.Parse(spec)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		d, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("SOCKS5 代理无效：%w", err)
		}
		return d.Dial, nil
	default:
		return nil, fmt.Errorf("FTP 不走 HTTP 代理，请改用 SOCKS5 或直连")
	}
}

func cloneTransport(base *http.Client) *http.Transport {
	if base != nil {
		if tr, ok := base.Transport.(*http.Transport); ok && tr != nil {
			return tr.Clone()
		}
	}
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		return tr.Clone()
	}
	return &http.Transport{}
}
