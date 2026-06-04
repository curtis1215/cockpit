package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/kardianos/service"
)

// buildSvcConfig constructs the kardianos service.Config for cockpit.
// mode must be "serve" or "agent".
// absCfg is the absolute path to the config file (used as -config argument).
func buildSvcConfig(mode, absCfg string) (*service.Config, error) {
	if mode != "serve" && mode != "agent" {
		return nil, fmt.Errorf("invalid mode %q: must be serve or agent", mode)
	}
	return &service.Config{
		Name:        "cockpit-" + mode,
		DisplayName: "Cockpit " + mode,
		Description: "cockpit homelab control plane (" + mode + ")",
		Arguments:   []string{mode, "-config", absCfg},
	}, nil
}

// program implements service.Interface for kardianos/service.
type program struct {
	mode   string
	cfgPath string
}

func (p *program) Start(s service.Service) error {
	go func() {
		switch p.mode {
		case "serve":
			runServe([]string{"-config", p.cfgPath})
		case "agent":
			runAgent([]string{"-config", p.cfgPath})
		default:
			log.Printf("service: unknown mode %q", p.mode)
		}
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	// Graceful shutdown is tracked as issue #8; use os.Exit for now.
	os.Exit(0)
	return nil
}

func runService(args []string) {
	fs := flag.NewFlagSet("service", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cockpit service <install|uninstall|start|stop|status> -mode <serve|agent> [-config <path>]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Manages cockpit as a system service (launchd on macOS, systemd on Linux).")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}

	mode := fs.String("mode", "", "serve or agent (required)")
	cfgPath := fs.String("config", "", "path to config JSON (required for install)")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fs.Usage()
		os.Exit(2)
	}
	action := fs.Arg(0)

	validActions := map[string]bool{
		"install": true, "uninstall": true,
		"start": true, "stop": true, "status": true,
	}
	if !validActions[action] {
		log.Fatalf("service: unknown action %q (want install|uninstall|start|stop|status)", action)
	}

	if *mode == "" {
		log.Fatal("service: -mode is required (serve or agent)")
	}

	// Resolve config path to absolute; required for install so the service
	// daemon can find the file regardless of working directory.
	absCfg := *cfgPath
	if action == "install" {
		if absCfg == "" {
			log.Fatal("service: -config is required for install")
		}
		var err error
		absCfg, err = filepath.Abs(absCfg)
		if err != nil {
			log.Fatalf("service: resolve config path: %v", err)
		}
		if _, err := os.Stat(absCfg); err != nil {
			log.Fatalf("service: config file %q does not exist: %v", absCfg, err)
		}
	}

	svcCfg, err := buildSvcConfig(*mode, absCfg)
	if err != nil {
		log.Fatalf("service: %v", err)
	}

	prg := &program{mode: *mode, cfgPath: absCfg}
	s, err := service.New(prg, svcCfg)
	if err != nil {
		log.Fatalf("service new: %v", err)
	}

	if action == "status" {
		st, err := s.Status()
		if err != nil {
			// kardianos returns an error for "not installed" on some platforms.
			if errors.Is(err, service.ErrNotInstalled) {
				fmt.Println("not installed")
				return
			}
			log.Fatalf("service status: %v", err)
		}
		switch st {
		case service.StatusRunning:
			fmt.Println("running")
		case service.StatusStopped:
			fmt.Println("stopped")
		default:
			fmt.Println("unknown")
		}
		return
	}

	if err := service.Control(s, action); err != nil {
		log.Fatalf("service %s: %v", action, err)
	}
	fmt.Printf("cockpit-%s: %s OK\n", *mode, action)
}
