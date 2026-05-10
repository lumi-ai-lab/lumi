package skills

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchPresetsUsesFreshCacheAndStaleFallback(t *testing.T) {
	cache := &presetsCache{}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			_ = json.NewEncoder(w).Encode(PresetsResponse{
				Version: 1,
				Skills:  []Preset{{Name: "find-skills", DisplayName: "Find Skills"}},
			})
			return
		}
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer server.Close()

	cache.url = server.URL
	first, err := cache.fetch()
	if err != nil {
		t.Fatalf("first fetch error = %v", err)
	}
	if first.Version != 1 || len(first.Skills) != 1 {
		t.Fatalf("first fetch = %+v", first)
	}

	second, err := cache.fetch()
	if err != nil {
		t.Fatalf("fresh cache fetch error = %v", err)
	}
	if second != first {
		t.Fatalf("fresh cache returned different pointer")
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 for fresh cache", requests)
	}

	cache.fetchedAt = time.Now().Add(-presetsCacheTTL - time.Minute)
	stale, err := cache.fetch()
	if err != nil {
		t.Fatalf("stale fallback fetch error = %v", err)
	}
	if stale != first {
		t.Fatalf("stale fallback returned different data")
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 after refresh attempt", requests)
	}
}
