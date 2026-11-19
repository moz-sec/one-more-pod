package main

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type Pod struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	CPU    int    `json:"cpu"`
	Memory int    `json:"memory"`
	Zone   string `json:"zone,omitempty"`
}
type Node struct {
	ID     string `json:"id"`
	CPU    int    `json:"cpu"`
	Memory int    `json:"memory"`
	Zone   string `json:"zone"`
}
type Action struct {
	Type   string `json:"type"`
	PodID  string `json:"podId"`
	NodeID string `json:"nodeId,omitempty"`
}
type Recipe struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Projects    []string `json:"projects"`
	Points      int      `json:"points"`
	Description string   `json:"description"`
}
type Game struct {
	mu         sync.Mutex `json:"-"`
	ID         string     `json:"gameId"`
	Challenge  string     `json:"challenge"`
	Nodes      []Node     `json:"nodes"`
	Pods       []Pod      `json:"pods"`
	Seed       uint32     `json:"seed"`
	Templates  []Pod      `json:"templates"`
	Stream     bool       `json:"stream"`
	Recipes    []Recipe   `json:"recipes"`
	Started    time.Time  `json:"-"`
	Done       bool       `json:"-"`
	Result     *Entry     `json:"-"`
	ResultHash [32]byte   `json:"-"`
}
type Entry struct {
	Player    string    `json:"player"`
	Score     int       `json:"score"`
	Scheduled int       `json:"scheduled"`
	Challenge string    `json:"challenge"`
	Created   time.Time `json:"createdAt"`
}
type Server struct {
	mu    sync.Mutex
	games map[string]*Game
	store scoreStore
}

var nodes = []Node{{"node-a", 8, 16, "east"}, {"node-b", 8, 16, "west"}, {"node-c", 8, 16, "east"}}
var templates = []Pod{{Name: "Envoy", Role: "プロキシ"}, {Name: "Prometheus", Role: "メトリクス"}, {Name: "OpenTelemetry", Role: "Collector"}, {Name: "Jaeger", Role: "トレース"}, {Name: "Fluent Bit", Role: "ログ収集", Zone: "east"}, {Name: "CoreDNS", Role: "DNS", Zone: "west"}}
var recipes = []Recipe{
	{ID: "tracing", Name: "トレース基盤", Projects: []string{"OpenTelemetry", "Jaeger"}, Points: 800, Description: "Collector と Jaeger でトレースを収集・分析"},
	{ID: "metrics", Name: "メトリクス基盤", Projects: []string{"OpenTelemetry", "Prometheus"}, Points: 800, Description: "Collector と Prometheus でメトリクスを収集"},
	{ID: "proxy-monitoring", Name: "プロキシ監視", Projects: []string{"Envoy", "Prometheus"}, Points: 600, Description: "Prometheus で Envoy のメトリクスを監視"},
	{ID: "dns-monitoring", Name: "DNS 監視", Projects: []string{"CoreDNS", "Prometheus"}, Points: 600, Description: "Prometheus で CoreDNS のメトリクスを監視"},
	{ID: "logs", Name: "ログ収集", Projects: []string{"Fluent Bit", "OpenTelemetry"}, Points: 600, Description: "Fluent Bit と Collector を使ったログ収集"},
}

func challenge(now time.Time) string { return now.UTC().Format("2006-01-02") }
func dailySeed(day string) uint32 {
	var seed uint32 = 2166136261
	for _, c := range []byte(day) {
		seed = (seed ^ uint32(c)) * 16777619
	}
	return seed
}
func streamPod(seed uint32, index int, choices []Pod) Pod {
	x := seed ^ (uint32(index+1) * 0x9e3779b9)
	next := func() uint32 { x ^= x << 13; x ^= x >> 17; x ^= x << 5; return x }
	p := choices[next()%uint32(len(choices))]
	p.ID = "pod-" + strconv.Itoa(index+1)
	p.CPU = 1 + int(next()%4)
	p.Memory = 2 + int(next()%7)
	return p
}
func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	var id [16]byte
	if _, err := cryptorand.Read(id[:]); err != nil {
		http.Error(w, "random unavailable", 500)
		return
	}
	now := time.Now()
	day := challenge(now)
	g := &Game{ID: hex.EncodeToString(id[:]), Challenge: day, Nodes: nodes, Seed: dailySeed(day), Templates: templates, Stream: true, Recipes: recipes, Started: now}
	s.mu.Lock()
	for k, v := range s.games {
		if now.Sub(v.Started) > 15*time.Minute {
			delete(s.games, k)
		}
	}
	if len(s.games) >= 10000 {
		s.mu.Unlock()
		http.Error(w, "server busy", 503)
		return
	}
	s.games[g.ID] = g
	s.mu.Unlock()
	write(w, g)
}
func score(g *Game, actions []Action) (int, int, bool) {
	if len(actions) > 8192 || (g.Stream && len(g.Templates) == 0) || (!g.Stream && len(actions) > len(g.Pods)*2) {
		return 0, 0, false
	}
	cpu := make([]int, len(g.Nodes))
	mem := make([]int, len(g.Nodes))
	placed := map[string]int{}
	pods := map[string]Pod{}
	for _, p := range g.Pods {
		pods[p.ID] = p
	}
	index, combo, bonus, evictions, scheduled := 0, 0, 0, 0, 0
	for _, a := range actions {
		if a.Type == "evict" {
			if (!g.Stream && index >= len(g.Pods)) || a.NodeID != "" {
				return 0, 0, false
			}
			n, ok := placed[a.PodID]
			if !ok {
				return 0, 0, false
			}
			p := pods[a.PodID]
			cpu[n] -= p.CPU
			mem[n] -= p.Memory
			delete(placed, a.PodID)
			evictions++
			combo = 0
			continue
		}
		if !g.Stream && index >= len(g.Pods) {
			return 0, 0, false
		}
		var p Pod
		if g.Stream {
			p = streamPod(g.Seed, index, g.Templates)
		} else {
			p = g.Pods[index]
		}
		if a.PodID != p.ID {
			return 0, 0, false
		}
		if a.Type == "skip" {
			if a.NodeID != "" {
				return 0, 0, false
			}
			index++
			combo = 0
			continue
		}
		if a.Type != "place" {
			return 0, 0, false
		}
		n := -1
		for j, node := range g.Nodes {
			if node.ID == a.NodeID {
				n = j
				break
			}
		}
		if n < 0 || (p.Zone != "" && p.Zone != g.Nodes[n].Zone) {
			return 0, 0, false
		}
		cpu[n] += p.CPU
		mem[n] += p.Memory
		if cpu[n] > g.Nodes[n].CPU || mem[n] > g.Nodes[n].Memory {
			return 0, 0, false
		}
		placed[p.ID] = n
		pods[p.ID] = p
		index++
		scheduled++
		combo++
		if combo > 1 {
			bonus += min(combo-1, 4) * 100
		}
	}
	if !g.Stream && index != len(g.Pods) {
		return 0, 0, false
	}
	points := len(placed)*1000 + scheduled*300 + bonus - evictions*200
	// Reward distinct cluster layouts, not just processing the pod queue.
	for i := range g.Nodes {
		if cpu[i] == g.Nodes[i].CPU || mem[i] == g.Nodes[i].Memory {
			points += 300
		}
	}
	if len(g.Nodes) == 3 && cpu[0] > 0 && cpu[1] > 0 && cpu[2] > 0 && max(cpu[0], cpu[1], cpu[2])-min(cpu[0], cpu[1], cpu[2]) <= 2 {
		points += 600
	}
	projects := make(map[string]bool)
	for id := range placed {
		projects[pods[id].Name] = true
	}
	for _, recipe := range g.Recipes {
		complete := true
		for _, name := range recipe.Projects {
			if !projects[name] {
				complete = false
				break
			}
		}
		if complete {
			points += recipe.Points
		}
	}
	if points < 0 {
		points = 0
	}
	return points, len(placed), true
}
func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GameID  string   `json:"gameId"`
		Player  string   `json:"player"`
		Actions []Action `json:"actions"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&body) != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	name := strings.TrimSpace(body.Player)
	if utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 20 {
		http.Error(w, "name must be 1–20 characters", 400)
		return
	}
	body.Player = name
	canonical, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	digest := sha256.Sum256(canonical)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stored, err := s.store.Find(ctx, body.GameID)
	if err == nil {
		if bytes.Equal(stored.Hash, digest[:]) {
			write(w, stored.Entry)
			return
		}
		http.Error(w, "game expired or already submitted", 400)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "leaderboard unavailable", http.StatusServiceUnavailable)
		return
	}
	s.mu.Lock()
	g := s.games[body.GameID]
	s.mu.Unlock()
	if g == nil {
		http.Error(w, "game expired or already submitted", 400)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Done {
		if g.Result != nil && g.ResultHash == digest {
			write(w, g.Result)
			return
		}
		http.Error(w, "game expired or already submitted", 400)
		return
	}
	if time.Since(g.Started) >= 5*time.Minute {
		http.Error(w, "game expired or already submitted", 400)
		return
	}
	points, scheduled, ok := score(g, body.Actions)
	if !ok {
		http.Error(w, "invalid actions", 400)
		return
	}
	e := Entry{name, points, scheduled, g.Challenge, time.Now().UTC()}
	stored, err = s.store.Save(ctx, g.ID, digest[:], e)
	if err != nil {
		http.Error(w, "leaderboard unavailable", http.StatusServiceUnavailable)
		return
	}
	if !bytes.Equal(stored.Hash, digest[:]) {
		http.Error(w, "game expired or already submitted", 400)
		return
	}
	g.Done = true
	g.Result = &stored.Entry
	g.ResultHash = digest
	write(w, stored.Entry)
}
func (s *Server) leaderboard(w http.ResponseWriter, r *http.Request) {
	day := ""
	if r.URL.Query().Get("period") == "today" {
		day = challenge(time.Now())
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	entries, err := s.store.List(ctx, day)
	if err != nil {
		http.Error(w, "leaderboard unavailable", http.StatusServiceUnavailable)
		return
	}
	write(w, entries)
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL is required")
	}
	var store *postgresStore
	for attempt := 0; attempt < 30; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var err error
		store, err = openPostgres(ctx, url)
		cancel()
		if err == nil {
			break
		}
		log.Printf("waiting for PostgreSQL (%d/30)", attempt+1)
		time.Sleep(2 * time.Second)
	}
	if store == nil {
		log.Fatal("PostgreSQL is unavailable")
	}
	defer store.db.Close()
	s := &Server{games: map[string]*Game{}, store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/games", s.start)
	mux.HandleFunc("POST /api/results", s.submit)
	mux.HandleFunc("GET /api/leaderboard", s.leaderboard)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if store.Ping(ctx) != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	dir := os.Getenv("STATIC_DIR")
	if dir == "" {
		dir = "web/dist"
	}
	mux.Handle("/", http.FileServer(http.Dir(dir)))
	log.Print("one-more-pod listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
