package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"omloader/internal/cache"
	"omloader/internal/config"
	"omloader/internal/simplestreams"
)

// ErrOffline is returned when the network is needed but offline mode (or policy.allow_online:false) forbids it.
var ErrOffline = errors.New("offline: not available in the cache")

// ErrDigest means downloaded bytes did not match the expected sha256 (the file is discarded).
var ErrDigest = errors.New("sha256 mismatch")

type Fetcher struct {
	Cfg     *config.Config
	Source  string
	Cache   *cache.Cache
	Offline bool // effective: --offline, cache.offline=on or policy.allow_online=false
	Warn    io.Writer
	Client  *http.Client
	Sleep   func(time.Duration)

	src    config.Source
	mu     sync.Mutex
	ranked []string
}

// New prepares a fetcher for a configured source; it enforces policy.allowed_sources.
func New(cfg *config.Config, source string, offline bool, warn io.Writer) (*Fetcher, error) {
	s, ok := cfg.Sources[source]
	if !ok {
		return nil, fmt.Errorf("unknown source %q", source)
	}
	if !cfg.SourceAllowed(source) {
		return nil, fmt.Errorf("source %q is not in policy.allowed_sources", source)
	}
	if s.Type != "simplestreams" && s.Type != "http" {
		return nil, fmt.Errorf("source %q has type %s: only simplestreams/http sources are supported by pull", source, s.Type)
	}
	if warn == nil {
		warn = io.Discard
	}
	cl, err := NewClient(cfg.Network, s.Proxy)
	if err != nil {
		return nil, err
	}
	return &Fetcher{Cfg: cfg, Source: source, src: s, Cache: &cache.Cache{Dir: cfg.Cache.Path}, Warn: warn, Client: cl, Sleep: time.Sleep,
		Offline: offline || cfg.Cache.Offline == "on" || !cfg.Policy.AllowOnline}, nil
}

func (f *Fetcher) rewrite(u string) string {
	for _, r := range f.src.Rewrite {
		if strings.HasPrefix(u, r.From) {
			return r.To + strings.TrimPrefix(u, r.From)
		}
	}
	return u
}

// bases returns primary + mirrors in configured order, filtered by the strategy (rewrite rules not yet applied).
func (f *Fetcher) bases() []string {
	m := f.Cfg.SourceMirrors(f.Source)
	if f.Cfg.MirrorPolicy.Strategy == "mirror-only" {
		return m
	}
	return append([]string{f.src.URL}, m...)
}

func join(base, rel string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(rel, "/")
}

// Probe is the health/timing result for one base URL.
type Probe struct {
	Base string
	URL  string
	OK   bool
	Dur  time.Duration
	Err  error
}

// ProbeAll checks each base (primary + mirrors; mirror-only: mirrors) with a HEAD request for the index (or the base itself
// for plain http sources).
func (f *Fetcher) ProbeAll(ctx context.Context) []Probe {
	bs := f.bases()
	out := make([]Probe, len(bs))
	var wg sync.WaitGroup
	for i, b := range bs {
		wg.Add(1)
		go func(i int, b string) {
			defer wg.Done()
			u := f.rewrite(b)
			if f.src.Type == "simplestreams" {
				u = f.rewrite(join(b, simplestreams.IndexPath))
			}
			p := Probe{Base: b, URL: u}
			t0 := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
			if err == nil {
				var resp *http.Response
				if resp, err = f.Client.Do(req); err == nil {
					resp.Body.Close()
					if resp.StatusCode >= 400 {
						err = fmt.Errorf("HTTP %d", resp.StatusCode)
					}
				}
			}
			p.Dur, p.Err, p.OK = time.Since(t0), err, err == nil
			out[i] = p
		}(i, b)
	}
	wg.Wait()
	return out
}

// order returns the bases in the order to try them.
func (f *Fetcher) order(ctx context.Context) []string {
	bs := f.bases()
	if f.Cfg.MirrorPolicy.Strategy != "fastest" || len(bs) < 2 {
		return bs
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ranked == nil {
		ps := f.ProbeAll(ctx)
		sort.SliceStable(ps, func(i, j int) bool {
			if ps[i].OK != ps[j].OK {
				return ps[i].OK
			}
			return ps[i].Dur < ps[j].Dur
		})
		for _, p := range ps {
			f.ranked = append(f.ranked, p.Base)
		}
	}
	return f.ranked
}

func (f *Fetcher) retries() int { return f.Cfg.Network.Retries }

func (f *Fetcher) backoff(i int) {
	if f.Sleep != nil {
		f.Sleep(time.Duration(i) * 500 * time.Millisecond)
	}
}

type httpStatusErr int

func (e httpStatusErr) Error() string { return fmt.Sprintf("HTTP %d", int(e)) }

// retriable: network errors and 5xx/429; not 4xx or digest mismatches.
func retriable(err error) bool {
	var hs httpStatusErr
	if errors.As(err, &hs) {
		return int(hs) >= 500 || int(hs) == 429
	}
	return !errors.Is(err, ErrDigest)
}

// MetaDoc fetches a small metadata document (index/catalog) honouring cache TTL/offline modes.
// Metadata is NOT signature-verified (no GPG); only downloaded blobs are digest-checked against it.
func (f *Fetcher) MetaDoc(ctx context.Context, rel string) ([]byte, error) {
	key := f.Source + "|" + rel
	cached, age, cerr := f.Cache.GetMeta(key)
	ttl := time.Duration(f.Cfg.MirrorPolicy.CacheTTLHours) * time.Hour
	if f.Offline {
		if cerr != nil {
			return nil, fmt.Errorf("%w: %s", ErrOffline, rel)
		}
		return cached, nil
	}
	if cerr == nil && f.Cfg.Cache.Offline == "auto" && age < ttl {
		return cached, nil
	}
	var last error
	for bi, b := range f.order(ctx) {
		u := f.rewrite(join(b, rel))
		var body []byte
		for try := 0; try <= f.retries(); try++ {
			if try > 0 {
				f.backoff(try)
			}
			body, last = f.getAll(ctx, u)
			if last == nil && !json.Valid(body) {
				last, body = fmt.Errorf("%s: not valid JSON", u), nil
			}
			if last == nil || !retriable(last) {
				break
			}
		}
		if last != nil {
			fmt.Fprintf(f.Warn, "warning: %v\n", last)
			continue
		}
		if bi > 0 || b != f.src.URL {
			fmt.Fprintf(f.Warn, "warning: metadata %s came from mirror %s and is not signature-verified; downloads are still sha256-checked against it\n", rel, b)
		}
		f.Cache.PutMeta(key, body)
		return body, nil
	}
	if cerr == nil {
		fmt.Fprintf(f.Warn, "warning: using cached %s (age %s) because every server failed: %v\n", rel, age.Round(time.Second), last)
		return cached, nil
	}
	if last == nil {
		last = errors.New("no mirror configured")
	}
	return nil, last
}

func (f *Fetcher) getAll(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %w", u, httpStatusErr(resp.StatusCode))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 256<<20))
}

// Blob returns the path of the cached file with the given sha256, downloading rel (primary/mirrors, with
// retries + Range resume) when missing. Every mirror's bytes are digest-verified; a mismatch discards the file
// and moves on to the next candidate.
func (f *Fetcher) Blob(ctx context.Context, rel, sha string, size int64) (string, error) {
	sha = strings.ToLower(sha)
	if len(sha) != 64 {
		return "", fmt.Errorf("%s: missing/invalid sha256 in the index", rel)
	}
	if f.Cache.Has(sha) {
		return f.Cache.Blob(sha), nil
	}
	if f.Offline {
		return "", fmt.Errorf("%w: %s (sha256 %s)", ErrOffline, rel, sha[:12])
	}
	var last error
	for _, b := range f.order(ctx) {
		u := f.rewrite(join(b, rel))
		if last = f.download(ctx, u, sha, size); last == nil {
			return f.Cache.Blob(sha), nil
		}
		fmt.Fprintf(f.Warn, "warning: %v\n", last)
	}
	if last == nil {
		last = errors.New("no server configured")
	}
	return "", last
}

func fileSHA(p string) (string, int64, error) {
	fh, err := os.Open(p)
	if err != nil {
		return "", 0, err
	}
	defer fh.Close()
	h := sha256.New()
	n, err := io.Copy(h, fh)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

func (f *Fetcher) download(ctx context.Context, u, sha string, size int64) error {
	part := f.Cache.Part(sha)
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		return err
	}
	var last error
	for try := 0; try <= f.retries(); try++ {
		if try > 0 {
			f.backoff(try)
		}
		last = f.attempt(ctx, u, part, size)
		if last == nil {
			got, _, err := fileSHA(part)
			if err != nil {
				return err
			}
			if got != sha {
				os.Remove(part)
				return fmt.Errorf("%s: %w: got %s want %s", u, ErrDigest, got, sha)
			}
			return f.Cache.Commit(part, sha)
		}
		if ctx.Err() != nil || !retriable(last) {
			break
		}
	}
	return last
}

// attempt appends to the part file, resuming with a Range request when it already has bytes.
func (f *Fetcher) attempt(ctx context.Context, u, part string, size int64) error {
	var off int64
	if st, err := os.Stat(part); err == nil {
		off = st.Size()
	}
	if size > 0 && off > size {
		os.Remove(part)
		off = 0
	}
	if size > 0 && off == size {
		return nil // complete, verify in caller
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC // server ignored Range: start over
	case http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part)
		return fmt.Errorf("%s: %w", u, httpStatusErr(500)) // retry from scratch
	default:
		return fmt.Errorf("%s: %w", u, httpStatusErr(resp.StatusCode))
	}
	fh, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(fh, resp.Body)
	if cerr := fh.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", u, err)
	}
	return nil
}
