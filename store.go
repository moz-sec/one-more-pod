package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type storedResult struct {
	Entry Entry
	Hash  []byte
}

type scoreStore interface {
	Find(context.Context, string) (storedResult, error)
	Save(context.Context, string, []byte, Entry) (storedResult, error)
	List(context.Context, string) ([]Entry, error)
	Ping(context.Context) error
}

type postgresStore struct{ db *sql.DB }

func openPostgres(ctx context.Context, url string) (*postgresStore, error) {
	db, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS kubetris_scores (
		game_id TEXT PRIMARY KEY,
		request_hash BYTEA NOT NULL,
		player TEXT NOT NULL,
		score INTEGER NOT NULL,
		scheduled INTEGER NOT NULL,
		challenge TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL
	)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS kubetris_scores_ranking_idx ON kubetris_scores (challenge, score DESC, created_at, game_id)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS kubetris_scores_all_time_idx ON kubetris_scores (score DESC, created_at, game_id)`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return &postgresStore{db: db}, nil
}

func (s *postgresStore) Find(ctx context.Context, id string) (storedResult, error) {
	var result storedResult
	err := s.db.QueryRowContext(ctx, `SELECT request_hash, player, score, scheduled, challenge, created_at FROM kubetris_scores WHERE game_id = $1`, id).
		Scan(&result.Hash, &result.Entry.Player, &result.Entry.Score, &result.Entry.Scheduled, &result.Entry.Challenge, &result.Entry.Created)
	return result, err
}

func (s *postgresStore) Save(ctx context.Context, id string, hash []byte, entry Entry) (storedResult, error) {
	var stored storedResult
	err := s.db.QueryRowContext(ctx, `INSERT INTO kubetris_scores (game_id, request_hash, player, score, scheduled, challenge, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (game_id) DO NOTHING
		RETURNING request_hash, player, score, scheduled, challenge, created_at`, id, hash, entry.Player, entry.Score, entry.Scheduled, entry.Challenge, entry.Created).
		Scan(&stored.Hash, &stored.Entry.Player, &stored.Entry.Score, &stored.Entry.Scheduled, &stored.Entry.Challenge, &stored.Entry.Created)
	if errors.Is(err, sql.ErrNoRows) {
		return s.Find(ctx, id)
	}
	return stored, err
}

func (s *postgresStore) List(ctx context.Context, day string) ([]Entry, error) {
	query := `SELECT player, score, scheduled, challenge, created_at FROM kubetris_scores`
	args := []any{}
	if day != "" {
		query += ` WHERE challenge = $1`
		args = append(args, day)
	}
	query += ` ORDER BY score DESC, created_at ASC, game_id ASC LIMIT 10`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.Player, &e.Score, &e.Scheduled, &e.Challenge, &e.Created); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func (s *postgresStore) Ping(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT 1 FROM kubetris_scores LIMIT 0`)
	if err != nil {
		return err
	}
	return rows.Close()
}
