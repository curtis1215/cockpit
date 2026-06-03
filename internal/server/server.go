package server

import (
	"encoding/json"
	"io/fs"
	"net/http"

	rootpkg "github.com/curtis1215/cockpit"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

type Server struct {
	st           *store.Store
	enrollSecret string
	inv          inventory.Inventory
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
	s.mux.HandleFunc("/api/systems", func(w http.ResponseWriter, r *http.Request) {
		list, err := s.st.ListSystems()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		if list == nil {
			list = []store.System{}
		}
		writeJSON(w, 200, list)
	})
	s.registerAgentAPI()
	s.registerVersionAPI()
	s.registerSSE()
	sub, _ := fs.Sub(rootpkg.Frontend, "cockpit_frontend")
	s.mux.Handle("/", http.FileServer(http.FS(sub)))
}
