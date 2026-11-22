package fetch

import (
	"bytes"
	"context"

	"omloader/internal/simplestreams"
)

// Catalog loads streams/v1/index.json and all image-download catalogs of the source (cached per cache.offline).
func (f *Fetcher) Catalog(ctx context.Context) (*simplestreams.Catalog, error) {
	b, err := f.MetaDoc(ctx, simplestreams.IndexPath)
	if err != nil {
		return nil, err
	}
	ix, err := simplestreams.ParseIndex(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	all := &simplestreams.Catalog{}
	for _, p := range ix.ImagePaths() {
		cb, err := f.MetaDoc(ctx, p)
		if err != nil {
			return nil, err
		}
		c, err := simplestreams.ParseCatalog(bytes.NewReader(cb))
		if err != nil {
			return nil, err
		}
		all.Merge(c)
	}
	return all, nil
}
