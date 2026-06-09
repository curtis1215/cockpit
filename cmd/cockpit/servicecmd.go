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
		Arguments:   []string{"service", "run", "-mode", mode, "-config", absCfg},
		Option:      service.KeyValue{"SystemdScript": systemdUnitTemplate},
	}, nil
}

// program implements service.Interface for kardianos/service.
type program struct {
	mode    string
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
	// 形式為 `cockpit service <action> -flags…`：flag 套件遇到第一個非 flag 參數即停止解析，
	// 因此先取出 action 再 parse 其餘參數。
	if len(args) < 1 || len(args[0]) == 0 || args[0][0] == '-' {
		fs.Usage()
		os.Exit(2)
	}
	action := args[0]
	fs.Parse(args[1:])

	validActions := map[string]bool{
		"install": true, "uninstall": true,
		"start": true, "stop": true, "status": true,
		"run": true, // 內部入口：由服務管理器啟動，呼叫 service.Run()（見下方）
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

	if action == "run" {
		// 系統服務管理器（Windows SCM / systemd / launchd）啟動服務時執行的入口。
		// s.Run() 在 Windows 進入 SCM dispatcher 並回報 RUNNING、處理停止控制；
		// 其他平台直接呼叫 program.Start 後阻塞等待停止訊號。
		// 缺此入口（直接跑 agent/serve）會導致 Windows SCM 1053 逾時。
		if err := s.Run(); err != nil {
			log.Fatalf("service run: %v", err)
		}
		return
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

// systemdUnitTemplate：複製 kardianos 預設模板、僅把 RestartSec 由 120 改為 5——
// agent 自我升級（self-exit）後 2 分鐘的重啟延遲體驗太差。
const systemdUnitTemplate = `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}} 
{{$dep}} {{end}}

[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .ChRoot}}RootDirectory={{.ChRoot|cmd}}{{end}}
{{if .WorkingDirectory}}WorkingDirectory={{.WorkingDirectory|cmdEscape}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
{{if .ReloadSignal}}ExecReload=/bin/kill -{{.ReloadSignal}} "$MAINPID"{{end}}
{{if .PIDFile}}PIDFile={{.PIDFile|cmd}}{{end}}
{{if and .LogOutput .HasOutputFileSupport -}}
StandardOutput=file:{{.LogDirectory}}/{{.Name}}.out
StandardError=file:{{.LogDirectory}}/{{.Name}}.err
{{- end}}
{{if gt .LimitNOFILE -1 }}LimitNOFILE={{.LimitNOFILE}}{{end}}
{{if .Restart}}Restart={{.Restart}}{{end}}
{{if .SuccessExitStatus}}SuccessExitStatus={{.SuccessExitStatus}}{{end}}
RestartSec=5
EnvironmentFile=-/etc/sysconfig/{{.Name}}

{{range $k, $v := .EnvVars -}}
Environment={{$k}}={{$v}}
{{end -}}

[Install]
WantedBy=multi-user.target
`
