package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

type QueryCount struct{
	Query string
	Count int64
}

func New(ctx context.Context, connString string) (*Store, error){

	pool, err := pgxpool.New(ctx , connString)

	if err!=nil {
		return nil , fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil , fmt.Errorf("ping: %w", err)
	}
	return &Store{pool : pool} , nil
}

func (s *Store) InitSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS queries (
			query TEXT PRIMARY KEY,
			count BIGINT NOT NULL
		)`)
	return err
}

func (s *Store) UpsertCounts(ctx context.Context , increments map[string]int) error {

	if len(increments) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for q , inc := range increments {
		batch.Queue(
			`INSERT INTO queries (query, count) VALUES ($1, $2)
			 ON CONFLICT (query) DO UPDATE SET count = queries.count + EXCLUDED.count`,
			q, inc,
		)
	}

	br:= s.pool.SendBatch(ctx, batch)
	defer br.Close()

	for range increments {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("batch exec: %w", err)
		}
	}
	return nil

}

func (s *Store) LoadAll(ctx context.Context , fn func(QueryCount) error) error{
	rows, err := s.pool.Query(ctx, `SELECT query, count FROM queries`)
	if err != nil {
		return fmt.Errorf("query all: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var qc QueryCount
		if err := rows.Scan(&qc.Query, &qc.Count); err != nil {
			return fmt.Errorf("scan: %w", err)
		}
		if err := fn(qc); err != nil { 
			return err
		}
	}
	return rows.Err()
}

func (s *Store) Close() {
	s.pool.Close()
}