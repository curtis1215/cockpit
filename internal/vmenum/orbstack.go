package vmenum

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"sync"
	"time"
)

// OrbStack 機器列舉：orbctl list --format json。
// 與 VMware 同管線回報（report-vms），uuid 用 orb 的 machine id（加 "orb-" 前綴，
// 不會與 SMBIOS UUID 比對撞型——normalizeUUID 對非 32-hex 直接回空，name 對帳照常作用）。

type orbMachine struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Image struct {
		Distro  string `json:"distro"`
		Version string `json:"version"`
	} `json:"image"`
}

var (
	orbOnce sync.Once
	orbPath string
)

// ResolvedOrbctl：解析 orbctl 絕對路徑（launchd/systemd 服務的 PATH 沒有 homebrew）。
func ResolvedOrbctl() string {
	orbOnce.Do(func() {
		orbPath = findOrbctl(nil, nil)
	})
	return orbPath
}

// findOrbctl：LookPath 優先，否則探測常見絕對路徑（與 findVmrun 同模式，可注入測試）。
func findOrbctl(lookPath func(string) (string, error), stat func(string) error) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if stat == nil {
		stat = func(p string) error { _, err := os.Stat(p); return err }
	}
	if p, err := lookPath("orbctl"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/orbctl", "/usr/local/bin/orbctl"} {
		if stat(p) == nil {
			return p
		}
	}
	return ""
}

func orbList() (string, error) {
	p := ResolvedOrbctl()
	if p == "" {
		return "", errNotFound{"orbctl"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "list", "--format", "json").Output()
	return string(out), err
}

type errNotFound struct{ name string }

func (e errNotFound) Error() string { return e.name + " not found" }

// parseOrbList：JSON → VM 列。
func parseOrbList(jsonOut string) []VM {
	var ms []orbMachine
	if err := json.Unmarshal([]byte(jsonOut), &ms); err != nil {
		return nil
	}
	var out []VM
	for _, m := range ms {
		if m.Name == "" {
			continue
		}
		state := "stopped"
		if m.State == "running" {
			state = "running"
		}
		gos := m.Image.Distro
		if m.Image.Version != "" {
			gos += "-" + m.Image.Version
		}
		out = append(out, VM{
			Name:    m.Name,
			UUID:    "orb-" + m.ID,
			State:   state,
			GuestOS: gos,
		})
	}
	return out
}
