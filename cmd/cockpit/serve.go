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

	tr := translate.New()
	refresh := func() { collector.RefreshUpstream(st, inv, collector.DefaultFetch, tr.Changelog) }
	srv.OnCheck(refresh)
	if len(inv.Software) > 0 {
		hours := cfg.CheckHours
		if hours <= 0 {
			hours = 24
		}
		go func() {
			for {
				refresh()
				time.Sleep(time.Duration(hours) * time.Hour)
			}
		}()
	}

	log.Printf("cockpit serve on http://%s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
