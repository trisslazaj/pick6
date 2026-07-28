// Package cache is a dumb disk cache: fetch a URL, keep the bytes, don't refetch today.
package cache

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Dir is where everything lands: ~/Library/Caches/pick6 on mac.
func Dir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "pick6")
	return d, os.MkdirAll(d, 0o755)
}

// Get returns the cached bytes for name, fetching from url if the cached copy is
// missing or older than maxAge. Reports whether the fetch actually hit the network.
func Get(name, url string, maxAge time.Duration) (data []byte, fetched bool, err error) {
	dir, err := Dir()
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(dir, name)

	if fi, statErr := os.Stat(path); statErr == nil && time.Since(fi.ModTime()) < maxAge {
		b, readErr := os.ReadFile(path)
		if readErr == nil {
			return b, false, nil
		}
	}

	b, err := download(url)
	if err != nil {
		// A stale cache beats no data at all — drafts happen offline sometimes.
		if b2, readErr := os.ReadFile(path); readErr == nil {
			return b2, false, nil
		}
		return nil, false, err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return nil, false, err
	}
	return b, true, nil
}

func download(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	// Some of these hosts are unfriendly to an empty UA.
	req.Header.Set("User-Agent", "pick6/0.1 (+https://github.com/trisslazaj/pick6)")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
