// Package fetch downloads files for online pull: proxy/CA aware HTTP client, mirrors with fallback/fastest,
// retries, Range resume, sha256 verification and a content-addressed cache (see internal/cache).
package fetch

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"omloader/internal/config"
)

// NoProxyMatch reports whether host (with optional :port) is covered by a no_proxy list
// (comma separated: "*", exact hosts, domain suffixes with or without leading dot, CIDRs).
func NoProxyMatch(list, host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	for _, e := range strings.Split(list, ",") {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if e == "*" || e == host {
			return true
		}
		if _, cidr, err := net.ParseCIDR(e); err == nil {
			if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		e = strings.TrimPrefix(e, "*")
		if strings.HasPrefix(e, ".") && strings.HasSuffix(host, e) || strings.HasSuffix(host, "."+e) {
			return true
		}
	}
	return false
}

// NewClient builds the HTTP client for a source. srcProxy overrides network.proxy ("direct" = no proxy).
// Without any configured proxy the standard HTTP(S)_PROXY / NO_PROXY environment is honoured.
func NewClient(n config.Network, srcProxy string) (*http.Client, error) {
	timeout := time.Duration(n.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if n.ExtraCA != "" {
		pem, err := os.ReadFile(n.ExtraCA)
		if err != nil {
			return nil, fmt.Errorf("network.extra_ca: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("network.extra_ca %s: no PEM certificates found", n.ExtraCA)
		}
	}
	px := n.Proxy
	if srcProxy != "" {
		px = srcProxy
	}
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DisableCompression:    true, // we hash the bytes as served; never transparently decode
	}
	switch px {
	case "":
		tr.Proxy = http.ProxyFromEnvironment
	case "direct":
	default:
		pu, err := url.Parse(px)
		if err != nil || pu.Host == "" {
			return nil, fmt.Errorf("bad proxy URL %q", px)
		}
		tr.Proxy = func(r *http.Request) (*url.URL, error) {
			if NoProxyMatch(n.NoProxy, r.URL.Host) {
				return nil, nil
			}
			return pu, nil
		}
	}
	return &http.Client{Transport: tr}, nil
}
