package skills

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	defaultPresetsURL          = "https://raw.githubusercontent.com/chenhg5/cc-connect/main/skill-presets.json"
	fallbackPresetsURL         = "https://gitee.com/chenhg5/cc-connect/raw/main/skill-presets.json"
	presetsCacheTTL            = 6 * time.Hour
	presetsHTTPTimeout         = 15 * time.Second
	presetsFallbackHTTPTimeout = 10 * time.Second
)

type presetsCache struct {
	mu        sync.RWMutex
	data      *PresetsResponse
	fetchedAt time.Time
	url       string
}

var globalPresetsCache = &presetsCache{}

func SetPresetsURL(url string) {
	globalPresetsCache.mu.Lock()
	defer globalPresetsCache.mu.Unlock()
	globalPresetsCache.url = url
	globalPresetsCache.data = nil
}

func FetchPresets() (*PresetsResponse, error) {
	return globalPresetsCache.fetch()
}

func (c *presetsCache) fetch() (*PresetsResponse, error) {
	c.mu.RLock()
	if c.data != nil && time.Since(c.fetchedAt) < presetsCacheTTL {
		defer c.mu.RUnlock()
		return c.data, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data != nil && time.Since(c.fetchedAt) < presetsCacheTTL {
		return c.data, nil
	}

	primaryURL := c.url
	if primaryURL == "" {
		primaryURL = defaultPresetsURL
	}
	result, err := fetchPresetsFromURL(primaryURL, presetsHTTPTimeout)
	if err != nil {
		slog.Warn("primary skill presets fetch failed, trying fallback", "url", primaryURL, "error", err)
		result, err = fetchPresetsFromURL(fallbackPresetsURL, presetsFallbackHTTPTimeout)
	}
	if err != nil {
		if c.data != nil {
			slog.Warn("all skill presets sources failed, using stale cache", "error", err)
			return c.data, nil
		}
		return nil, fmt.Errorf("fetch skill presets: %w", err)
	}
	c.data = result
	c.fetchedAt = time.Now()
	return c.data, nil
}

func fetchPresetsFromURL(url string, timeout time.Duration) (*PresetsResponse, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP GET %s: status %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read body from %s: %w", url, err)
	}

	var result PresetsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse JSON from %s: %w", url, err)
	}
	return &result, nil
}
