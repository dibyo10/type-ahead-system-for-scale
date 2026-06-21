package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"typeahead/internal/buffer"
	"typeahead/internal/cache"
	"typeahead/internal/trending"
	"typeahead/internal/trie"
)


type API struct {
	Trie     *trie.Trie
	Cache    *cache.Cache
	Buffer   *buffer.Buffer
	Trending *trending.Scorer
	Weight   float64 
}


func (a *API) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /suggest", a.handleSuggest)
	mux.HandleFunc("POST /search", a.handleSearch)
	mux.HandleFunc("GET /stats", a.handleStats)
	mux.HandleFunc("GET /cache/debug", a.handleCacheDebug)
	mux.HandleFunc("GET /cache/stats", a.handleCacheStats)
	return mux
}

func (a *API) handleSuggest(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	mode := r.URL.Query().Get("mode")

	
	if mode == "trending" {
		base := a.Trie.Search(prefix)
		scored := make([]trending.Scored, len(base))
		for i, s := range base {
			scored[i] = trending.Scored{Query: s.Query, Count: s.Count}
		}
		reranked := a.Trending.Rerank(scored, a.Weight)

		out := make([]trie.Suggestion, len(reranked))
		for i, s := range reranked {
			out[i] = trie.Suggestion{Query: s.Query, Count: s.Count}
		}
		writeJSON(w, out)
		return
	}

	
	if suggestions, hit := a.Cache.Get(prefix); hit {
		writeJSON(w, suggestions)
		return
	}
	suggestions := a.Trie.Search(prefix)
	if suggestions == nil {
		suggestions = []trie.Suggestion{}
	}
	a.Cache.Set(prefix, suggestions)
	writeJSON(w, suggestions)
}

func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	query := strings.ToLower(strings.TrimSpace(body.Query))
	if query == "" {
		http.Error(w, "empty query", http.StatusBadRequest)
		return
	}

	a.Buffer.Add(query)     
	a.Trending.Record(query) 

	writeJSON(w, map[string]string{"message": "Searched"})
}

func (a *API) handleStats(w http.ResponseWriter, r *http.Request) {
	adds, flushes, writes := a.Buffer.Stats()
	writeJSON(w, map[string]int{
		"searches_received": adds,
		"db_flushes":        flushes,
		"rows_written":      writes,
	})
}

func (a *API) handleCacheDebug(w http.ResponseWriter, r *http.Request) {
	prefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("prefix")))
	owner, hit := a.Cache.Debug(prefix)
	writeJSON(w, map[string]any{
		"prefix": prefix,
		"node":   owner,
		"hit":    hit,
	})
}

func (a *API) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, a.Cache.AllStats())
}


func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}