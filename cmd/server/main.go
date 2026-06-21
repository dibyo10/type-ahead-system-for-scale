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
	"typeahead/internal/store"
	"typeahead/internal/trie"
)

func main() {
	ctx := context.Background()
	connStr := "postgres://typeahead:typeahead@localhost:5433/typeahead?sslmode=disable"

	// --- connect to Postgres (source of truth) ---
	st, err := store.New(ctx, connStr)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	// --- build the trie from Postgres at startup ---
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

	// --- write buffer: batches search submissions before hitting Postgres ---
	// flush every 5s OR when 1000 distinct queries pile up, whichever first.
	buf := buffer.New(st, 1000, 5*time.Second)

	// --- HTTP routes ---
	mux := http.NewServeMux()

	mux.HandleFunc("GET /suggest", func(w http.ResponseWriter, r *http.Request) {
		// lowercase so "IP" and "ip" hit the same lowercase trie path
		prefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

		suggestions := t.Search(prefix)
		if suggestions == nil {
			suggestions = []trie.Suggestion{} // return [] not null in JSON
		}

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

		// Record the search into the buffer (cheap, no DB wait), then return
		// the dummy response immediately. The real DB write happens later, batched.
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

	// --- start the server in a background goroutine ---
	// We don't use log.Fatal(ListenAndServe(...)) directly anymore, because
	// that blocks forever and our shutdown/flush code below would never run.
	srv := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		log.Println("listening on :8080")
		// ErrServerClosed is the normal, expected error when we shut down on purpose.
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	// --- wait for Ctrl+C / kill, then shut down cleanly ---
	// This blocks until the OS sends an interrupt or terminate signal.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	// Stop accepting new requests, give in-flight ones a moment to finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)

	// Final flush of anything still buffered, so we don't lose it on a clean exit.
	buf.Close()
	log.Println("bye")
}