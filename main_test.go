package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu      sync.Mutex
	results map[string]storedResult
}

func newMemoryStore() *memoryStore { return &memoryStore{results: map[string]storedResult{}} }
func (s *memoryStore) Find(_ context.Context, id string) (storedResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.results[id]
	if !ok {
		return storedResult{}, sql.ErrNoRows
	}
	return r, nil
}
func (s *memoryStore) Save(_ context.Context, id string, hash []byte, e Entry) (storedResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.results[id]; ok {
		return r, nil
	}
	r := storedResult{Entry: e, Hash: append([]byte(nil), hash...)}
	s.results[id] = r
	return r, nil
}
func (s *memoryStore) List(_ context.Context, day string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := []Entry{}
	for _, r := range s.results {
		if day == "" || r.Entry.Challenge == day {
			entries = append(entries, r.Entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Score > entries[j].Score })
	if len(entries) > 10 {
		entries = entries[:10]
	}
	return entries, nil
}
func (s *memoryStore) Ping(context.Context) error { return nil }

type failingStore struct {
	scoreStore
	failFind bool
}

func (s failingStore) Find(ctx context.Context, id string) (storedResult, error) {
	if s.failFind {
		return storedResult{}, errors.New("database unavailable")
	}
	return s.scoreStore.Find(ctx, id)
}
func (s failingStore) Save(context.Context, string, []byte, Entry) (storedResult, error) {
	return storedResult{}, errors.New("database unavailable")
}

func TestSharedPodStreamVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/pod-stream.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Day  string `json:"day"`
		Seed uint32 `json:"seed"`
		Pods []struct {
			Index  int    `json:"index"`
			Name   string `json:"name"`
			CPU    int    `json:"cpu"`
			Memory int    `json:"memory"`
		} `json:"pods"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		if dailySeed(vector.Day) != vector.Seed {
			t.Fatalf("wrong seed for %s", vector.Day)
		}
		for _, expected := range vector.Pods {
			p := streamPod(vector.Seed, expected.Index, templates)
			if p.Name != expected.Name || p.CPU != expected.CPU || p.Memory != expected.Memory {
				t.Fatalf("%s pod %d: got %+v want %+v", vector.Day, expected.Index, p, expected)
			}
		}
	}
}

func TestSharedScoreVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/score.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name    string   `json:"name"`
		Seed    uint32   `json:"seed"`
		Actions []Action `json:"actions"`
		Score   int      `json:"score"`
		Running int      `json:"running"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		g := Game{Stream: true, Seed: v.Seed, Templates: templates, Nodes: nodes, Recipes: recipes}
		points, running, valid := score(&g, v.Actions)
		if !valid || points != v.Score || running != v.Running {
			t.Errorf("%s: score=%d running=%d valid=%v", v.Name, points, running, valid)
		}
	}
}

func TestPodStreamDeterminismAndSizes(t *testing.T) {
	seed := dailySeed("2026-01-01")
	sizes := map[[2]int]bool{}
	for i := 0; i < 1000; i++ {
		p := streamPod(seed, i, templates)
		if p != streamPod(seed, i, templates) {
			t.Fatalf("pod %d is not deterministic", i)
		}
		if p.CPU < 1 || p.CPU > 4 || p.Memory < 2 || p.Memory > 8 {
			t.Fatalf("invalid size: %+v", p)
		}
		sizes[[2]int{p.CPU, p.Memory}] = true
	}
	if len(sizes) < 2 || streamPod(seed, 0, templates) == streamPod(dailySeed("2026-01-02"), 0, templates) {
		t.Fatal("expected variable challenges")
	}
}

func TestStreamReplayBeyondTwelve(t *testing.T) {
	g := Game{Stream: true, Seed: dailySeed("2026-01-01"), Templates: templates, Nodes: nodes}
	actions := make([]Action, 20)
	for i := range actions {
		actions[i] = Action{Type: "skip", PodID: streamPod(g.Seed, i, g.Templates).ID}
	}
	if _, _, ok := score(&g, actions); !ok {
		t.Fatal("valid stream rejected")
	}
	actions[15].PodID = actions[14].PodID
	if _, _, ok := score(&g, actions); ok {
		t.Fatal("out of order stream accepted")
	}
}

func TestResultValidation(t *testing.T) {
	store := newMemoryStore()
	s := Server{games: map[string]*Game{}, store: store}
	g := &Game{ID: "test", Challenge: "2026-01-01", Started: time.Now(), Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 1, Memory: 1, Zone: "east"}}}
	s.games[g.ID] = g
	send := func(actions []Action) int {
		body, _ := json.Marshal(map[string]any{"gameId": "test", "player": "player", "actions": actions})
		w := httptest.NewRecorder()
		s.submit(w, httptest.NewRequest("POST", "/api/results", bytes.NewReader(body)))
		return w.Code
	}
	if got := send([]Action{{Type: "place", PodID: "a", NodeID: "node-b"}}); got != 400 {
		t.Fatalf("invalid zone: %d", got)
	}
	if got := send([]Action{{Type: "place", PodID: "a", NodeID: "node-a"}}); got != 200 {
		t.Fatalf("valid result: %d", got)
	}
	if got := send([]Action{{Type: "place", PodID: "a", NodeID: "node-a"}}); got != 200 {
		t.Fatalf("retry of same result: %d", got)
	}
	if got := send([]Action{{Type: "skip", PodID: "a"}}); got != 400 {
		t.Fatalf("changed retry: %d", got)
	}
	delete(s.games, "test") // A completed result remains retryable after an app restart.
	if got := send([]Action{{Type: "place", PodID: "a", NodeID: "node-a"}}); got != 200 {
		t.Fatalf("retry after restart: %d", got)
	}
	if len(store.results) != 1 || store.results["test"].Entry.Score != 1300 {
		t.Fatalf("unexpected scores: %+v", store.results)
	}
}

func TestDatabaseFailureDoesNotConsumeGame(t *testing.T) {
	for _, failFind := range []bool{true, false} {
		store := newMemoryStore()
		g := &Game{ID: "failure", Started: time.Now(), Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 1, Memory: 1}}}
		s := Server{games: map[string]*Game{g.ID: g}, store: failingStore{scoreStore: store, failFind: failFind}}
		body, _ := json.Marshal(map[string]any{"gameId": g.ID, "player": "player", "actions": []Action{{Type: "place", PodID: "a", NodeID: "node-a"}}})
		w := httptest.NewRecorder()
		s.submit(w, httptest.NewRequest("POST", "/api/results", bytes.NewReader(body)))
		if w.Code != 503 || g.Done || len(store.results) != 0 {
			t.Fatalf("failFind=%v code=%d done=%v", failFind, w.Code, g.Done)
		}
		s.store = store
		w = httptest.NewRecorder()
		s.submit(w, httptest.NewRequest("POST", "/api/results", bytes.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("retry after failure: %d", w.Code)
		}
	}
}

func TestResultDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		want    int
	}{
		{"within registration window", 61 * time.Second, 200},
		{"after registration window", 301 * time.Second, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &Game{ID: "deadline", Started: time.Now().Add(-tc.elapsed), Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 1, Memory: 1}}}
			s := Server{games: map[string]*Game{g.ID: g}, store: newMemoryStore()}
			body, _ := json.Marshal(map[string]any{"gameId": g.ID, "player": "player", "actions": []Action{{Type: "place", PodID: "a", NodeID: "node-a"}}})
			w := httptest.NewRecorder()
			s.submit(w, httptest.NewRequest("POST", "/api/results", bytes.NewReader(body)))
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestConcurrentResultRetry(t *testing.T) {
	g := &Game{ID: "concurrent", Started: time.Now(), Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 1, Memory: 1}}}
	store := newMemoryStore()
	s := Server{games: map[string]*Game{g.ID: g}, store: store}
	body, _ := json.Marshal(map[string]any{"gameId": g.ID, "player": "player", "actions": []Action{{Type: "place", PodID: "a", NodeID: "node-a"}}})
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			s.submit(w, httptest.NewRequest("POST", "/api/results", bytes.NewReader(body)))
			codes <- w.Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("retry returned %d", code)
		}
	}
	if len(store.results) != 1 {
		t.Fatalf("got %d leaderboard entries", len(store.results))
	}
}

func TestReplaySkipEvictAndCombo(t *testing.T) {
	g := Game{Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 2, Memory: 4}, {ID: "b", CPU: 2, Memory: 4}, {ID: "c", CPU: 2, Memory: 4}, {ID: "d", CPU: 2, Memory: 4}}}
	actions := []Action{{Type: "place", PodID: "a", NodeID: "node-a"}, {Type: "place", PodID: "b", NodeID: "node-a"}, {Type: "evict", PodID: "a"}, {Type: "skip", PodID: "c"}, {Type: "place", PodID: "d", NodeID: "node-a"}}
	points, scheduled, ok := score(&g, actions)
	if !ok || points != 2800 || scheduled != 2 {
		t.Fatalf("score=%d scheduled=%d valid=%v", points, scheduled, ok)
	}
	invalid := append([]Action{}, actions...)
	invalid[2].PodID = "c"
	if _, _, ok := score(&g, invalid); ok {
		t.Fatal("eviction of an unplaced pod accepted")
	}
	invalid = append([]Action{}, actions...)
	invalid[3].PodID = "d"
	if _, _, ok := score(&g, invalid); ok {
		t.Fatal("out of order skip accepted")
	}
}

func TestReplacingPodRewardsThroughput(t *testing.T) {
	g := Game{Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 1, Memory: 2}, {ID: "b", CPU: 1, Memory: 2}}}
	keep := []Action{{Type: "place", PodID: "a", NodeID: "node-a"}, {Type: "skip", PodID: "b"}}
	replace := []Action{{Type: "place", PodID: "a", NodeID: "node-a"}, {Type: "evict", PodID: "a"}, {Type: "place", PodID: "b", NodeID: "node-a"}}
	kept, _, ok := score(&g, keep)
	replaced, running, replacementOK := score(&g, replace)
	if !ok || !replacementOK || kept != 1300 || replaced != 1400 || running != 1 {
		t.Fatalf("keep=%d replace=%d running=%d valid=%v/%v", kept, replaced, running, ok, replacementOK)
	}
}

func TestLayoutBonuses(t *testing.T) {
	g := Game{Nodes: nodes, Pods: []Pod{{ID: "a", CPU: 8, Memory: 2}, {ID: "b", CPU: 8, Memory: 2}, {ID: "c", CPU: 8, Memory: 2}}}
	actions := []Action{{Type: "place", PodID: "a", NodeID: "node-a"}, {Type: "place", PodID: "b", NodeID: "node-b"}, {Type: "place", PodID: "c", NodeID: "node-c"}}
	points, count, ok := score(&g, actions)
	if !ok || count != 3 || points != 5700 {
		t.Fatalf("score=%d count=%d valid=%v", points, count, ok)
	}
	actions[2].Type = "skip"
	actions[2].NodeID = ""
	points, count, ok = score(&g, actions)
	if !ok || count != 2 || points != 3300 {
		t.Fatalf("score after skip=%d count=%d valid=%v", points, count, ok)
	}
}

func TestServiceCombosUseFinalProjects(t *testing.T) {
	g := Game{Nodes: nodes, Recipes: recipes, Pods: []Pod{
		{ID: "a", Name: "OpenTelemetry", CPU: 1, Memory: 2},
		{ID: "b", Name: "OpenTelemetry", CPU: 1, Memory: 2},
		{ID: "c", Name: "Jaeger", CPU: 1, Memory: 2},
		{ID: "d", Name: "Prometheus", CPU: 1, Memory: 2},
		{ID: "e", Name: "Envoy", CPU: 1, Memory: 2},
		{ID: "f", Name: "CoreDNS", CPU: 1, Memory: 2},
	}}
	actions := []Action{{Type: "place", PodID: "a", NodeID: "node-a"}, {Type: "place", PodID: "b", NodeID: "node-b"}, {Type: "place", PodID: "c", NodeID: "node-c"}, {Type: "place", PodID: "d", NodeID: "node-a"}, {Type: "place", PodID: "e", NodeID: "node-b"}, {Type: "evict", PodID: "a"}, {Type: "skip", PodID: "f"}}
	points, _, ok := score(&g, actions)
	base := Game{Nodes: g.Nodes, Pods: g.Pods}
	basePoints, _, baseOK := score(&base, actions)
	if !ok || !baseOK || points-basePoints != 2200 {
		t.Fatalf("duplicate project must preserve all three combos: %d - %d", points, basePoints)
	}
	actions = append(actions[:len(actions)-1], Action{Type: "evict", PodID: "b"}, Action{Type: "skip", PodID: "f"})
	points, _, ok = score(&g, actions)
	basePoints, _, baseOK = score(&base, actions)
	if !ok || !baseOK || points-basePoints != 600 {
		t.Fatalf("evicting last collector must remove two combos: %d - %d", points, basePoints)
	}
}

func TestEveryProjectParticipatesInServiceCombo(t *testing.T) {
	for _, project := range templates {
		found := false
		for _, recipe := range recipes {
			for _, name := range recipe.Projects {
				if name == project.Name {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("project %s has no service combo", project.Name)
		}
	}
}
