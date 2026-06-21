package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"typeahead/internal/store"
)

func main(){
	filePath := flag.String("file", "", "path to AOL collection .txt file")
	defaultDB := os.Getenv("DATABASE_URL")
	if defaultDB == "" {
		defaultDB = "postgres://typeahead:typeahead@localhost:5433/typeahead?sslmode=disable"
	}
	connStr := flag.String("db", defaultDB, "postgres connection string")
	flag.Parse()

	if *filePath == "" {
		log.Fatal("need -file=path/to/aol.txt")
	}

	ctx := context.Background()

	st, err := store.New(ctx, *connStr)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(ctx); err != nil {
		log.Fatalf("schema: %v", err)
	}


	f, err := os.Open(*filePath)
	if err != nil {
		log.Fatalf("open file: %v", err)
	}
	defer f.Close()

	counts := make(map[string]int)
	scanner := bufio.NewScanner(f)

	start := time.Now()
	lineNo:=0

	for scanner.Scan() {
		lineNo++
		if lineNo == 1 {
			continue 
		}

		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) < 2 {
			continue 
		}

		query := strings.ToLower(strings.TrimSpace(fields[1]))
		if query == "" || query == "-" {
			continue
		}
        counts[query]++
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("scan: %v", err)
	}

	fmt.Printf("read %d lines, %d unique queries in %s\n",
		lineNo-1, len(counts), time.Since(start))
	
	const chunkSize = 5000
	chunk := make(map[string]int, chunkSize)
	written := 0
	flush := func() {
		if len(chunk) == 0 {
			return
		}
		if err := st.UpsertCounts(ctx, chunk); err != nil {
			log.Fatalf("upsert: %v", err)
		}
		written += len(chunk)
		chunk = make(map[string]int, chunkSize) 
	}

	for q, c := range counts {
		chunk[q] = c
		if len(chunk) >= chunkSize {
			flush()
		}
	}
	flush() 

	fmt.Printf("wrote %d unique queries to postgres in %s\n", written, time.Since(start))


}