package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/curtis1215/cockpit/internal/collector"
	"github.com/curtis1215/cockpit/internal/config"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/server"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/translate"
)

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfgPath := fs.String("config", "/etc/cockpit/serve.json", "serve config json")
	fs.Parse(args)

	cfg, err := config.LoadServe(*cfgPath)
	if err != nil {
		log.Fatalf("serve config: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	inv := inventory.Inventory{}
	if cfg.InventoryPath != "" {
		inv, err = inventory.Load(cfg.InventoryPath)
		if err != nil {
			log.Fatalf("inventory: %v", err)
		}
	}
	srv := server.NewWithInventory(st, cfg.EnrollSecret, inv)
	srv.SetInventoryPath(cfg.InventoryPath)
	srv.SetVersion(version)

	tr := translate.NewWithCmd(cfg.TranslateCmd)
	// 用 srv.Inventory()（熱載後快照）而非啟動時的 inv——否則 UI 後加的軟體永遠不會被刷新。
	refresh := func() { collector.RefreshUpstream(st, srv.Inventory(), collector.DefaultFetch, tr.Changelog) }
	srv.OnCheck(refresh)
	if true { // 排程恆啟動：軟體可能於執行期經 UI 加入

		hours := cfg.CheckHours
		if hours <= 0 {
			hours = 24
		}
		go func() {
			for {
				refresh()
				// 通知所有 agent 重新回報版本（與 handleCheck 相同邏輯）
				inv2 := srv.Inventory()
				for name := range inv2.Machines {
					st.SetCheckRequested(name)
				}
				if systems, err := st.ListSystems(); err == nil {
					for _, sys := range systems {
						st.SetCheckRequested(sys.Label)
					}
				}
				time.Sleep(time.Duration(hours) * time.Hour)
			}
		}()
	}

	go func() {
		for {
			time.Sleep(5 * time.Minute)
			now := time.Now().Unix()
			if err := st.Downsample(now); err != nil {
				log.Printf("downsample: %v", err)
			}
			if err := st.PruneMetrics(now); err != nil {
				log.Printf("prune: %v", err)
			}
		}
	}()

	log.Printf("cockpit serve on http://%s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
