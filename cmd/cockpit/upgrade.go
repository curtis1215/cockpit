package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/curtis1215/cockpit/internal/selfupdate"
)

func runUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cockpit upgrade [-repo owner/repo]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Checks GitHub Releases for a newer cockpit binary and replaces")
		fmt.Fprintln(os.Stderr, "the running executable in-place.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}

	defaultRepo := os.Getenv("COCKPIT_REPO")
	if defaultRepo == "" {
		defaultRepo = "curtis1215/cockpit"
	}
	repo := fs.String("repo", defaultRepo, "GitHub repo (owner/repo)")
	fs.Parse(args)

	hc := &http.Client{Timeout: 60 * time.Second}
	if err := selfupdate.Run(hc, "https://api.github.com", *repo, version, ""); err != nil {
		log.Fatalf("upgrade: %v", err)
	}
}
