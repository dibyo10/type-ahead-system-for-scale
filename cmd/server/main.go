package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

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

	
	mux := http.NewServeMux()
	mux.HandleFunc("GET /suggest", func(w http.ResponseWriter, r *http.Request) {
		
		prefix := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

		suggestions := t.Search(prefix) 
		if suggestions == nil {
			suggestions = []trie.Suggestion{} 
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(suggestions)
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}