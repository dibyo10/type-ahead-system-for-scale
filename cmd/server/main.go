package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"typeahead/internal/buffer"
	"typeahead/internal/cache"
	"typeahead/internal/store"
	"typeahead/internal/trie"
)

func main() {
	ctx := context.Background()
	connStr := "postgres://typeahead:typeahead@localhost:5433/typeahead?sslmode=disable"

	
	st, err := store.New(ctx, connStr)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	
	t := trie.New(10)
	start := time.Now()
	loaded := 0
	err = st.LoadAll(ctx, func(qc store.QueryCount) error {
		t.Insert(qc.Query, qc.Count)
		loaded++
		return nil
	})
	if err != nil {
		log.Fatalf("load: %v", err)
	}
	t.Build()
	log.Printf("loaded %d queries, built trie in %s", loaded, time.Since(start))

	
	buf := buffer.New(st, 1000, 5*time.Second)

	
	c := cache.New(3, 100, 60*time.Second)

	
	mux := http.NewServeMux()

	mux.HandleFunc("GET /suggest", func(w http.ResponseWriter, r *http.Request) {
		prefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

		
		if suggestions, hit := c.Get(prefix); hit {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(suggestions)
			return
		}

		
		suggestions := t.Search(prefix)
		if suggestions == nil {
			suggestions = []trie.Suggestion{}
		}
		c.Set(prefix, suggestions) 

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(suggestions)
	})

	mux.HandleFunc("POST /search", func(w http.ResponseWriter, r *http.Request) {
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

		buf.Add(query)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Searched"})
	})

	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		adds, flushes, writes := buf.Stats()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]int{
			"searches_received": adds,
			"db_flushes":        flushes,
			"rows_written":      writes,
		})
	})

	mux.HandleFunc("GET /cache/debug", func(w http.ResponseWriter, r *http.Request) {
		prefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("prefix")))
		owner, hit := c.Debug(prefix)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"prefix": prefix,
			"node":   owner,
			"hit":    hit,
		})
	})

	mux.HandleFunc("GET /cache/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c.AllStats())
	})

	
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Println("listening on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)

	buf.Close()
	log.Println("bye")
}