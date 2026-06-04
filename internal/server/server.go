package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"sync"

	rootpkg "github.com/curtis1215/cockpit"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

type Server struct {
	st           *store.Store
	enrollSecret string
	invMu        sync.RWMutex
	inv          inventory.Inventory
	invPath      string
	onCheck      func()
	mux          *http.ServeMux
}

func New(st *store.Store, enrollSecret string) *Server {
	return NewWithInventory(st, enrollSecret, inventory.Inventory{})
}

func NewWithInventory(st *store.Store, enrollSecret string, inv inventory.Inventory) *Server {
	s := &Server{st: st, enrollSecret: enrollSecret, inv: inv, mux: http.NewServeMux()}
	s.routes()
	return s
}

// SetInventoryPath sets the path for persistent inventory writeback.
func (s *Server) SetInventoryPath(p string) {
	s.invMu.Lock()
	defer s.invMu.Unlock()
	s.invPath = p
}

// getInv returns a snapshot of the current inventory under RLock.
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
	s.mux.HandleFunc("/api/systems", s.apiSystemsEnriched)
	s.registerAgentAPI()
	s.registerAgentVT()
	s.registerMonitorAPI()
	s.registerVersionAPI()
	s.registerManageAPI()
	s.registerSSE()
	sub, _ := fs.Sub(rootpkg.Frontend, "cockpit_frontend")
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
}
