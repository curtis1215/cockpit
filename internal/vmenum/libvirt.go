package vmenum

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// libvirt/KVM 機器列舉：virsh -c qemu:///system。
// 先 `list --all --name` 取得名單，逐台 `dominfo` 解析 UUID/State/CPU/記憶體。
// KVM 的 domain UUID 會原樣成為 guest 的 SMBIOS product_uuid，
// uuidMatch 直接相等（或 smbios swap）即可自動對帳。

var (
	virshOnce sync.Once
	virshPath string
)

// ResolvedVirsh：解析 virsh 絕對路徑（systemd 服務的 PATH 可能不含使用者路徑）。
func ResolvedVirsh() string {
	virshOnce.Do(func() {
		virshPath = findVirsh(nil, nil)
	})
	return virshPath
}

// findVirsh：LookPath 優先，否則探測常見絕對路徑（與 findOrbctl 同模式，可注入測試）。
func findVirsh(lookPath func(string) (string, error), stat func(string) error) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if stat == nil {
		stat = func(p string) error { _, err := os.Stat(p); return err }
	}
	if p, err := lookPath("virsh"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/bin/virsh", "/usr/local/bin/virsh"} {
		if stat(p) == nil {
			return p
		}
	}
	return ""
}

// virshRun：真實執行 virsh。固定 qemu:///system（agent 以 root 跑，session 不適用）
// 與 LC_ALL=C（dominfo 的 key 為英文、輸出穩定可解析）。
func virshRun(args ...string) (string, error) {
	p := ResolvedVirsh()
	if p == "" {
		return "", errNotFound{"virsh"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p, append([]string{"-c", "qemu:///system"}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}

// enumerateLibvirt：取得 domain 名單後逐台 dominfo。任一台失敗跳過該台不中斷。
func enumerateLibvirt(run func(args ...string) (string, error)) []VM {
	namesOut, err := run("list", "--all", "--name")
	if err != nil {
		return nil
	}
	var out []VM
	for _, name := range strings.Split(namesOut, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		info, ierr := run("dominfo", name)
		if ierr != nil {
			continue
		}
		out = append(out, parseDominfo(name, info))
	}
	return out
}

// parseDominfo：解析 virsh dominfo 的「Key: value」行（LC_ALL=C 下 key 固定英文）。
func parseDominfo(name, info string) VM {
	vm := VM{Name: name, State: "stopped"}
	for _, line := range strings.Split(info, "\n") {
		i := strings.Index(line, ":")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.TrimSpace(line[i+1:])
		switch key {
		case "UUID":
			vm.UUID = val
		case "State":
			if val == "running" {
				vm.State = "running"
			}
		case "CPU(s)":
			vm.VCPU, _ = strconv.Atoi(val)
		case "Max memory":
			if kib, ok := strings.CutSuffix(val, " KiB"); ok {
				if n, err := strconv.Atoi(kib); err == nil {
					vm.RamMB = n / 1024
				}
			}
		}
	}
	return vm
}
