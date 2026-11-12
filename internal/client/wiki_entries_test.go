package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// wikiServer serves n pages of 10 bytes each, a part of at most limit (else 3) at a time.
func wikiServer(t *testing.T, n int, stuck bool) *Client {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		from, _ := strconv.Atoi(r.URL.Query().Get("after"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit == 0 || limit > 3 {
			limit = 3
		}
		var items []store.WikiEntry
		for i := from; i < n && len(items) < limit; i++ {
			items = append(items, store.WikiEntry{Repo: "/r", Slug: "p" + strconv.Itoa(i), Body: strings.Repeat("x", 10)})
		}
		next := ""
		if end := from + len(items); end < n {
			next = strconv.Itoa(end)
		}
		if stuck {
			next = r.URL.Query().Get("after") // a cursor that does not move
			if next == "" {
				next = "0"
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "next": next})
	}))
	t.Cleanup(h.Close)
	c, err := New(h.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Every wiki's pages are read part by part to the end; a bound of pages or of text stops it and
// says some were left out; a cursor that does not move ends it rather than looping.
func TestAllWikiEntries(t *testing.T) {
	ctx := context.Background()
	all, err := wikiServer(t, 8, false).AllWikiEntries(ctx, 100, 1<<20)
	if err != nil || len(all.Pages) != 8 || all.More || all.Pages[7].Slug != "p7" {
		t.Fatalf("%+v %v", all, err)
	}
	all, _ = wikiServer(t, 8, false).AllWikiEntries(ctx, 5, 1<<20)
	if len(all.Pages) != 5 || !all.More {
		t.Fatalf("by count: %+v", all)
	}
	all, _ = wikiServer(t, 6, false).AllWikiEntries(ctx, 6, 1<<20)
	if len(all.Pages) != 6 || all.More {
		t.Fatalf("exactly the count: %+v", all)
	}
	all, _ = wikiServer(t, 8, false).AllWikiEntries(ctx, 100, 45)
	if len(all.Pages) != 4 || !all.More {
		t.Fatalf("by size: %+v", all)
	}
	all, err = wikiServer(t, 8, true).AllWikiEntries(ctx, 100, 1<<20)
	if err != nil || len(all.Pages) > 6 {
		t.Fatalf("stuck cursor: %d %v", len(all.Pages), err)
	}
}
