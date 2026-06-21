package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"typeahead/internal/api"
	"typeahead/internal/buffer"
	"typeahead/internal/cache"
	"typeahead/internal/store"
	"typeahead/internal/trending"
	"typeahead/internal/trie"
)

func main() {
	ctx := context.Background()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://typeahead:typeahead@localhost:5433/typeahead?sslmode=disable"
	}

	
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
	
	
	tr := trending.New(30 * time.Second)

	
	apiHandler := &api.API{
		Trie:     t,
		Cache:    c,
		Buffer:   buf,
		Trending: tr,
		Weight:   5000.0,
	}
	mux := apiHandler.Routes()
	mux.Handle("/", http.FileServer(http.Dir("web")))

	
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