package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/curtis1215/cockpit/internal/config"
	"github.com/curtis1215/cockpit/internal/server"
	"github.com/curtis1215/cockpit/internal/store"
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
	srv := server.New(st, cfg.EnrollSecret)
	log.Printf("cockpit serve on http://%s", cfg.Listen)
	if err := http.ListenAndServe(cfg.Listen, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}
