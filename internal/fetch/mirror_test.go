package fetch

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omloader/internal/config"
)

// A TUNA/NJU-like server: metadata lives in /lxc-images/meta/simplestreams/v1/, streams/v1 is a redirect (tuna) or 404 (nju),
// files are under /lxc-images/images/.
func lxcMirror(redirect bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case strings.HasPrefix(p, "/lxc-images/streams/v1/") && redirect:
			http.Redirect(w, r, strings.Replace(p, "streams/v1", "meta/simplestreams/v1", 1), http.StatusFound)
		case p == "/lxc-images/meta/simplestreams/v1/index.json":
			w.Write([]byte(`{"format":"index:1.0","index":{"i":{"datatype":"image-downloads","path":"streams/v1/images.json"}}}`))
		case p == "/lxc-images/meta/simplestreams/v1/images.json":
			w.Write([]byte(`{"products":{"a:b:amd64:default":{"os":"a"}}}`))
		case p == "/lxc-images/images/f.bin":
			http.ServeContent(w, r, "f.bin", time.Time{}, strings.NewReader(string(payload)))
		default:
			http.NotFound(w, r)
		}
	}))
}

func withServers(t *testing.T, official string, mirrorSel string, servers map[string]config.Mirror) *Fetcher {
	t.Helper()
	c := mkCfg(t, official)
	s := c.Sources["lxc-images"]
	s.Servers, s.Mirror = servers, mirrorSel
	c.Sources["lxc-images"] = s
	return newF(t, c, false)
}

func TestMirrorDifferentBasePathAndRewrite(t *testing.T) {
	srv := lxcMirror(false) // like NJU: streams/v1 is a 404, only meta/simplestreams exists
	defer srv.Close()
	rw := []config.Rewrite{{From: "streams/v1/", To: "meta/simplestreams/v1/"}}
	f := withServers(t, deadURL(), "nju", map[string]config.Mirror{"nju": {URL: srv.URL, Path: "/lxc-images/", Rewrite: rw}})
	cat, err := f.Catalog(context.Background())
	if err != nil || len(cat.Products) != 1 {
		t.Fatalf("catalog: %v %v", cat, err)
	}
	if _, err := f.Blob(context.Background(), "images/f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
	// without the rewrite the NJU-like layout is a 404 for the index
	f = withServers(t, deadURL(), "nju", map[string]config.Mirror{"nju": {URL: srv.URL + "/lxc-images"}})
	if _, err := f.Catalog(context.Background()); err == nil {
		t.Fatal("expected failure without rewrite")
	}
}

func TestMirrorRedirectFollowed(t *testing.T) {
	srv := lxcMirror(true) // like TUNA: 302 streams/v1 -> meta/simplestreams/v1
	defer srv.Close()
	f := withServers(t, deadURL(), "tuna", map[string]config.Mirror{"tuna": {URL: srv.URL + "/lxc-images"}})
	if cat, err := f.Catalog(context.Background()); err != nil || len(cat.Products) != 1 {
		t.Fatalf("%v %v", cat, err)
	}
	if _, err := f.Blob(context.Background(), "images/f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
}

func TestNamedMirrorFirstThenOfficial(t *testing.T) {
	var hOff, hMir int32
	off, mir := fileServer(&hOff), fileServer(&hMir)
	defer off.Close()
	defer mir.Close()
	f := withServers(t, off.URL, "m", map[string]config.Mirror{"m": {URL: mir.URL}})
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil || hOff != 0 || hMir == 0 {
		t.Fatal(err, hOff, hMir)
	}
	// dead named mirror: falls back to the official server
	f = withServers(t, off.URL, "m", map[string]config.Mirror{"m": {URL: deadURL()}})
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil || hOff == 0 {
		t.Fatal(err, hOff)
	}
	// --mirror override + unknown name
	if err := f.SetMirror("official"); err != nil {
		t.Fatal(err)
	}
	if err := f.SetMirror("nope"); err == nil || !strings.Contains(err.Error(), "no mirror") {
		t.Fatalf("%v", err)
	}
}

func TestAutoPicksFastestWithFallback(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Write(payload)
	}))
	defer slow.Close()
	var hf int32
	fast := fileServer(&hf)
	defer fast.Close()
	f := withServers(t, slow.URL, "auto", map[string]config.Mirror{"fast": {URL: fast.URL}, "dead": {URL: deadURL()}})
	got := f.order(context.Background())
	if len(got) != 3 || got[0].Name != "fast" || got[2].Name != "dead" {
		t.Fatalf("order %+v", got)
	}
	ps := f.ProbeAll(context.Background())
	if len(ps) != 3 || ps[0].Name != "official" {
		t.Fatalf("%+v", ps)
	}
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil || hf == 0 {
		t.Fatal(err, hf)
	}
	// everything dead but one -> still works
	f = withServers(t, deadURL(), "auto", map[string]config.Mirror{"a": {URL: deadURL()}, "ok": {URL: fast.URL}})
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil {
		t.Fatal(err)
	}
}

func TestProbeRegistry(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(403)
	}))
	defer ok.Close()
	c := mkCfg(t, "x")
	c.Sources["hub"] = config.Source{Type: "oci-registry", URL: deadURL(), Mirror: "auto",
		Servers: map[string]config.Mirror{"p": {URL: strings.TrimPrefix(ok.URL, "http://")}}}
	c.Network.TimeoutSec = 2
	// our test server speaks plain http; ProbeRegistry uses https, so the probe fails with a TLS error: just check wiring
	ps, err := ProbeRegistry(context.Background(), c, "hub", "")
	if err != nil || len(ps) != 2 || ps[1].Name != "p" || !strings.HasPrefix(ps[1].URL, "https://127.0.0.1") {
		t.Fatalf("%+v %v", ps, err)
	}
	if _, err := ProbeRegistry(context.Background(), c, "hub", "zzz"); err == nil {
		t.Fatal("unknown mirror")
	}
	_ = io.Discard
}

func TestUserAgentSent(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ua = r.Header.Get("User-Agent"); w.Write(payload) }))
	defer srv.Close()
	f := withServers(t, srv.URL, "", nil)
	if _, err := f.Blob(context.Background(), "f.bin", sum(payload), 0); err != nil || ua != UserAgent {
		t.Fatal(err, ua)
	}
}
