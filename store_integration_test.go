package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"
)

// Run against a disposable database with TEST_DATABASE_URL set.
func TestPostgresRankingPersistence(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := openPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	key := "test-" + hex.EncodeToString(random[:])
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), "DELETE FROM kubetris_scores WHERE challenge=$1", key)
		_ = store.db.Close()
	})
	first := Entry{Player: "player", Score: 1300, Scheduled: 1, Challenge: key, Created: time.Now().UTC()}
	got, err := store.Save(ctx, key, []byte("first"), first)
	if err != nil || got.Entry.Score != first.Score {
		t.Fatalf("save: %+v %v", got, err)
	}
	again, err := store.Save(ctx, key, []byte("different"), Entry{Player: "other", Score: 9999, Challenge: key, Created: time.Now()})
	if err != nil || again.Entry.Score != 1300 || !bytes.Equal(again.Hash, []byte("first")) {
		t.Fatalf("conflict changed result: %+v %v", again, err)
	}
	second, err := openPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer second.db.Close()
	stored, err := second.Find(ctx, key)
	if err != nil || stored.Entry.Player != "player" || stored.Entry.Score != 1300 {
		t.Fatalf("read after reconnect: %+v %v", stored, err)
	}
	entries, err := second.List(ctx, key)
	if err != nil || len(entries) != 1 || entries[0].Score != 1300 {
		t.Fatalf("ranking: %+v %v", entries, err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := second.Save(ctx, key+"-concurrent", []byte("same"), first)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent save: %v", err)
		}
	}
	entries, err = second.List(ctx, key)
	if err != nil || len(entries) != 2 {
		t.Fatalf("concurrent saves produced duplicate rows: %+v %v", entries, err)
	}
}
