package docker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// Only the default engine builder's cache is requested. Never scan container or
// volume filesystems for cache accounting, and omit Dockerfile descriptions.
type BuildCacheRecord struct {
	ID, CreatedAt, LastUsedAt string
	Size, UsageCount          int64
	InUse, Shared             bool
}

func (c *Client) BuildCache(ctx context.Context) ([]BuildCacheRecord, error) {
	defer c.http.CloseIdleConnections()
 request, err := http.NewRequestWithContext(ctx, "GET", "http://docker/system/df?type=build-cache", nil)
	if err != nil {
		return nil, err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, errors.New("Cannot read default engine build cache")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("Engine build cache unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	var data struct{ BuildCache []BuildCacheRecord }
	if err != nil || len(raw) > 16<<20 || json.Unmarshal(raw, &data) != nil || len(data.BuildCache) > 10000 {
		return nil, errors.New("Invalid or oversized engine build cache")
	}
	return data.BuildCache, nil
}
