package fetch

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"omloader/internal/config"
)

// ProbeRegistry checks the servers of an oci-registry source (the official host and its Hub proxies; mirror selects them
// like for simplestreams sources) with GET https://host/v2/. 200 and 401 (token auth required) both mean "registry alive".
func ProbeRegistry(ctx context.Context, cfg *config.Config, source, mirror string) ([]Probe, error) {
	bs, err := cfg.SourceBases(source, mirror)
	if err != nil {
		return nil, err
	}
	cl, err := NewClient(cfg.Network, cfg.Sources[source].Proxy)
	if err != nil {
		return nil, err
	}
	out := make([]Probe, len(bs))
	var wg sync.WaitGroup
	for i, b := range bs {
		wg.Add(1)
		go func(i int, b config.Base) {
			defer wg.Done()
			u := "https://" + config.RegistryHost(b.URL) + "/v2/"
			p := Probe{Name: b.Name, Base: b.URL, URL: u}
			t0 := time.Now()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err == nil {
				var resp *http.Response
				if resp, err = cl.Do(req); err == nil {
					resp.Body.Close()
					if resp.StatusCode != 200 && resp.StatusCode != 401 {
						err = fmt.Errorf("HTTP %d", resp.StatusCode)
					}
				}
			}
			p.Dur, p.Err, p.OK = time.Since(t0), err, err == nil
			out[i] = p
		}(i, b)
	}
	wg.Wait()
	return out, nil
}
