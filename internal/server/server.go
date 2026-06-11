package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	rootpkg "github.com/curtis1215/cockpit"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

type Server struct {
	st              *store.Store
	enrollSecret    string
	invMu           sync.RWMutex
	inv             inventory.Inventory
	invPath         string
	onCheck         func()
	mux             *http.ServeMux
	version         string
	latestFn        func() (string, error)
	upgradeFn       func() (bool, error)
	exitFn          func()
	writableCheckFn func() error
	latestMu        sync.Mutex
	latestCache     string
	latestAt        time.Time
	upgrading       atomic.Bool
	// boot 與 jobSeen 供孤兒 job reaper 使用：jobSeen 記錄每個 running job
	// 最後一次 agent 活動（claim / log / control）；server 重啟後沒有紀錄的
	// running job 以 boot 為基準起算。
	boot    time.Time
	jobSeen sync.Map // map[int64]time.Time
}

func New(st *store.Store, enrollSecret string) *Server {
	return NewWithInventory(st, enrollSecret, inventory.Inventory{})
}

func NewWithInventory(st *store.Store, enrollSecret string, inv inventory.Inventory) *Server {
	s := &Server{st: st, enrollSecret: enrollSecret, inv: inv, mux: http.NewServeMux(), boot: time.Now()}
	s.latestFn = defaultLatestFn()
	s.upgradeFn = func() (bool, error) {
		return defaultUpgrade(s.version)
	}
	s.exitFn = func() { os.Exit(0) }
	s.writableCheckFn = defaultWritableCheck
	s.routes()
	return s
}

// SetVersion stores the server binary version string, exposed via /api/version.
func (s *Server) SetVersion(v string) { s.version = v }

// SetInventoryPath sets the path for persistent inventory writeback.
func (s *Server) SetInventoryPath(p string) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	s.invPath = p
}

// getInv returns a snapshot of the current inventory under RLock.
// Inventory 回傳當前（熱載後）的 inventory 快照，供外部（serve 的 refresh 排程）使用。
func (s *Server) Inventory() inventory.Inventory { return s.getInv() }

func (s *Server) getInv() inventory.Inventory {
	s.invMu.RLock()
	defer s.invMu.RUnlock()
	return s.inv
}

// setInv replaces the inventory. If persist=true and invPath is set, writes to disk.
func (s *Server) setInv(inv inventory.Inventory, persist bool) error {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	s.inv = inv
	if persist && s.invPath != "" {
		b, err := inventory.Marshal(inv)
		if err != nil {
			return err
		}
		if err := os.WriteFile(s.invPath, b, 0644); err != nil {
			return err
		}
	}
	return nil
}

// OnCheck 注入「重新整理上游版本」回呼（serve 端提供，避免 server 依賴 collector）。
func (s *Server) OnCheck(f func()) { s.onCheck = f }

func (s *Server) Handler() http.Handler { return s.mux }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	s.mux.HandleFunc("/api/version", s.apiVersion)
	s.mux.HandleFunc("/api/server/upgrade", s.apiServerUpgrade)
	s.mux.HandleFunc("/api/systems", s.apiSystemsEnriched)
	s.registerAgentAPI()
	s.registerAgentVT()
	s.registerMonitorAPI()
	s.registerVersionAPI()
	s.registerManageAPI()
	s.registerTranslateAPI()
	s.registerSSE()
	sub, _ := fs.Sub(rootpkg.Frontend, "cockpit_frontend")
	static := http.FileServer(http.FS(sub))
	// 以 server 版本當 ETag：升級後快取立即失效；未升級時回 304。
	// 沒有這層，瀏覽器/CDN 會啟發式快取 .js/.css，發版後 UI 卡舊版。
	s.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag := `"` + s.version + `"`
		if s.version != "" && r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if s.version != "" {
			w.Header().Set("ETag", etag)
		}
		w.Header().Set("Cache-Control", "no-cache")
		static.ServeHTTP(w, r)
	}))
}
