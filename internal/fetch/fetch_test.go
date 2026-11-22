package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"omloader/internal/config"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

var payload = []byte(strings.Repeat("omloader-test-payload-", 5000))

func mkCfg(t *testing.T, urls ...string) *config.Config {
	t.Helper()
	r, err := config.Load("", "")
	if err != nil {
		t.Fatal(err)
	}
	c := &r.Config
	c.Cache.Path = t.TempDir()
	c.Network.Retries = 1
	c.Network.TimeoutSec = 5
	c.Sources = map[string]config.Source{"lxc-images": {Type: "simplestreams", URL: urls[0], Mirrors: urls[1:]}}
	return c
}

func newF(t *testing.T, c *config.Config, offline bool) *Fetcher {
	t.Helper()
	f, err := New(c, "lxc-images", offline, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	f.Sleep = func(time.Duration) {}
	return f
}

func fileServer(hits *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			atomic.AddInt32(hits, 1)
		}
		switch r.URL.Path {
		case "/f.bin":
			http.ServeContent(w, r, "f.bin", time.Time{}, strings.NewReader(string(payload)))
		case "/streams/v1/index.json":
			w.Write([]byte(`{"format":"index:1.0","index":{"i":{"datatype":"image-downloads","path":"streams/v1/images.json"}}}`))
		case "/streams/v1/images.json":
			w.Write([]byte(`{"products":{}}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func deadURL() string {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	u := "http://" + l.Addr().String()
	l.Close()
	return u
}

func TestFallbackToMirrorAndCache(t *testing.T) {
	var hits int32
	good := fileServer(&hits)
	defer good.Close()
	c := mkCfg(t, deadURL(), good.URL)
	f := newF(t, c, false)
	p, err := f.Blob(context.Background(), "f.bin", sum(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(payload) {
		t.Fatal("content")
	}
	if !strings.Contains(p, filepath.Join("blobs", sum(payload)[:2])) {
		t.Fatal(p)
	}
	h := atomic.LoadInt32(&hits)
	// second call: served from the cache, no network
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil || atomic.LoadInt32(&hits) != h {
		t.Fatal("expected cache hit", err)
	}
}

func TestDigestMismatchNeverTrustsMirror(t *testing.T) {
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("evil bytes")) }))
	defer evil.Close()
	good := fileServer(nil)
	defer good.Close()
	// mismatching primary, good mirror -> falls through to the mirror
	f := newF(t, mkCfg(t, evil.URL, good.URL), false)
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
	// only the evil one -> error, nothing cached
	c := mkCfg(t, evil.URL)
	f = newF(t, c, false)
	_, err := f.Blob(context.Background(), "f.bin", sum(payload), 0)
	if !errors.Is(err, ErrDigest) {
		t.Fatalf("want ErrDigest, got %v", err)
	}
	if f.Cache.Has(sum(payload)) {
		t.Fatal("bad file cached")
	}
	if _, err := os.Stat(f.Cache.Part(sum(payload))); err == nil {
		t.Fatal("part file left behind")
	}
}

func TestResume(t *testing.T) {
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		http.ServeContent(w, r, "f", time.Time{}, strings.NewReader(string(payload)))
	}))
	defer srv.Close()
	f := newF(t, mkCfg(t, srv.URL), false)
	part := f.Cache.Part(sum(payload))
	os.MkdirAll(filepath.Dir(part), 0o755)
	os.WriteFile(part, payload[:1000], 0o644)
	p, err := f.Blob(context.Background(), "f", sum(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if len(ranges) != 1 || ranges[0] != "bytes=1000-" {
		t.Fatalf("ranges %v", ranges)
	}
	if b, _ := os.ReadFile(p); string(b) != string(payload) {
		t.Fatal("content after resume")
	}
}

func TestRetryOn5xx(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			http.Error(w, "boom", 503)
			return
		}
		w.Write(payload)
	}))
	defer srv.Close()
	f := newF(t, mkCfg(t, srv.URL), false)
	if _, err := f.Blob(context.Background(), "x", sum(payload), 0); err != nil || n != 2 {
		t.Fatal(err, n)
	}
}

func TestOfflineModes(t *testing.T) {
	var hits int32
	srv := fileServer(&hits)
	defer srv.Close()
	c := mkCfg(t, srv.URL)
	f := newF(t, c, false)
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := atomic.LoadInt32(&hits)
	// --offline: cached blob + cached metadata work without touching the network; a miss errors
	fo := newF(t, c, true)
	if _, err := fo.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := fo.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := fo.Blob(context.Background(), "other", sum([]byte("zz")), 0); !errors.Is(err, ErrOffline) {
		t.Fatal("want ErrOffline", err)
	}
	// cache.offline: on and policy.allow_online:false behave the same
	c.Cache.Offline = "on"
	if f2 := newF(t, c, false); !f2.Offline {
		t.Fatal("cache.offline on")
	}
	c.Cache.Offline, c.Policy.AllowOnline = "auto", false
	if f3 := newF(t, c, false); !f3.Offline {
		t.Fatal("allow_online false")
	}
	if atomic.LoadInt32(&hits) != h {
		t.Fatal("network used in offline mode")
	}
	// cache.offline: off always refetches metadata
	c.Policy.AllowOnline, c.Cache.Offline = true, "off"
	f4 := newF(t, c, false)
	f4.Catalog(context.Background())
	if atomic.LoadInt32(&hits) == h {
		t.Fatal("offline=off must refetch")
	}
	// auto + fresh cache: no refetch; server gone + stale/fresh cache: still works
	c.Cache.Offline = "auto"
	f5 := newF(t, c, false)
	h = atomic.LoadInt32(&hits)
	f5.Catalog(context.Background())
	if atomic.LoadInt32(&hits) != h {
		t.Fatal("auto must use fresh cache")
	}
	c.MirrorPolicy.CacheTTLHours = 0
	srv.Close()
	if _, err := newF(t, c, false).Catalog(context.Background()); err != nil {
		t.Fatal("stale cache fallback:", err)
	}
}

func TestPolicyAllowedSources(t *testing.T) {
	c := mkCfg(t, "http://x")
	c.Sources["other"] = config.Source{Type: "simplestreams", URL: "http://y"}
	c.Policy.AllowedSources = []string{"other"}
	if _, err := New(c, "lxc-images", false, nil); err == nil || !strings.Contains(err.Error(), "allowed_sources") {
		t.Fatal(err)
	}
	if _, err := New(c, "other", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := New(c, "nope", false, nil); err == nil {
		t.Fatal("unknown")
	}
}

func TestStrategies(t *testing.T) {
	var hp, hm int32
	prim := fileServer(&hp)
	mir := fileServer(&hm)
	defer prim.Close()
	defer mir.Close()
	c := mkCfg(t, prim.URL, mir.URL)
	c.MirrorPolicy.Strategy = "mirror-only"
	f := newF(t, c, false)
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil || hp != 0 || hm == 0 {
		t.Fatal(err, hp, hm)
	}
	c = mkCfg(t, prim.URL)
	c.MirrorPolicy.Strategy = "mirror-only"
	if _, err := newF(t, c, false).Blob(context.Background(), "f.bin", sum(payload), 0); err == nil {
		t.Fatal("mirror-only without mirrors must fail")
	}
	// fastest: a slow primary loses against a quick mirror
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write(payload)
	}))
	defer slow.Close()
	var hf int32
	fast := fileServer(&hf)
	defer fast.Close()
	c = mkCfg(t, slow.URL, fast.URL)
	c.MirrorPolicy.Strategy = "fastest"
	f = newF(t, c, false)
	if got := f.order(context.Background()); got[0] != fast.URL {
		t.Fatalf("order %v", got)
	}
	ps := f.ProbeAll(context.Background())
	if len(ps) != 2 || ps[1].Err != nil || !ps[0].OK && ps[0].Err == nil {
		t.Fatalf("%+v", ps)
	}
}

func TestRewrite(t *testing.T) {
	srv := fileServer(nil)
	defer srv.Close()
	c := mkCfg(t, "http://does-not-resolve.invalid")
	s := c.Sources["lxc-images"]
	s.Rewrite = []config.Rewrite{{From: "http://does-not-resolve.invalid", To: srv.URL}}
	c.Sources["lxc-images"] = s
	if _, err := newF(t, c, false).Blob(context.Background(), "f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
}

// local CONNECT proxy; used with a TLS origin whose CA is supplied through network.extra_ca.
func TestProxyAndExtraCA(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer origin.Close()
	var connects int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "connect only", 405)
			return
		}
		atomic.AddInt32(&connects, 1)
		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		cl, buf, _ := w.(http.Hijacker).Hijack()
		fmt.Fprint(buf, "HTTP/1.1 200 Connection established\r\n\r\n")
		buf.Flush()
		go func() { io.Copy(up, cl); up.Close() }()
		go func() { io.Copy(cl, up); cl.Close() }()
	}))
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: origin.Certificate().Raw}), 0o644)

	c := mkCfg(t, origin.URL)
	// without extra CA: TLS verification fails (system pool does not know the test CA)
	if _, err := newF(t, c, false).Blob(context.Background(), "x", sum(payload), 0); err == nil {
		t.Fatal("expected TLS failure without extra_ca")
	}
	c.Network.ExtraCA = ca
	c.Network.Proxy = proxy.URL
	if _, err := newF(t, c, false).Blob(context.Background(), "x", sum(payload), 0); err != nil || connects == 0 {
		t.Fatal("via proxy:", err, connects)
	}
	// no_proxy bypasses the proxy (dead proxy address would fail otherwise)
	c = mkCfg(t, origin.URL)
	c.Network.ExtraCA, c.Network.Proxy, c.Network.NoProxy = ca, "http://127.0.0.1:1", "127.0.0.1"
	if _, err := newF(t, c, false).Blob(context.Background(), "x", sum(payload), 0); err != nil {
		t.Fatal("no_proxy:", err)
	}
	// per-source proxy override "direct"
	c = mkCfg(t, origin.URL)
	c.Network.ExtraCA, c.Network.Proxy = ca, "http://127.0.0.1:1"
	s := c.Sources["lxc-images"]
	s.Proxy = "direct"
	c.Sources["lxc-images"] = s
	if _, err := newF(t, c, false).Blob(context.Background(), "x", sum(payload), 0); err != nil {
		t.Fatal("direct:", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.pem")
	os.WriteFile(bad, []byte("junk"), 0o644)
	if _, err := NewClient(config.Network{ExtraCA: bad}, ""); err == nil {
		t.Fatal("junk CA accepted")
	}
}

func TestNoProxyMatch(t *testing.T) {
	for _, tc := range []struct {
		l, h string
		w    bool
	}{{"", "a.com", false}, {"*", "a.com", true}, {"a.com", "a.com:80", true}, {".a.com", "x.a.com", true}, {"a.com", "x.a.com", true},
		{"10.0.0.0/8", "10.1.2.3:443", true}, {"10.0.0.0/8", "11.1.2.3", false}, {"b.com", "ab.com", false}} {
		if NoProxyMatch(tc.l, tc.h) != tc.w {
			t.Errorf("%q %q", tc.l, tc.h)
		}
	}
}
