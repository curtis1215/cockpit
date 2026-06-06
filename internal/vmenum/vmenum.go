package vmenum

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// VM holds info about a single VMware Fusion virtual machine.
type VM struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	VmxPath string `json:"vmx_path"`
	State   string `json:"state"`
	VCPU    int    `json:"vcpu"`
	RamMB   int    `json:"ram_mb"`
	GuestOS string `json:"guest_os"`
}

// Enumerator enumerates VMware Fusion VMs. All fields are injectable for testing.
type Enumerator struct {
	RunVmrun func() (string, error)
	Glob     func() []string
	ReadFile func(p string) (string, error)
	RunOrb   func() (string, error)             // 注入測試用；nil = 真實 orbctl
	RunVirsh func(args ...string) (string, error) // 注入測試用；nil = 真實 virsh
}

// vmrunOnce caches the resolved vmrun path so we only search once per process.
var (
	vmrunOnce sync.Once
	vmrunPath string // absolute path, or "" if not found
)

// VmrunCandidates is the ordered list of well-known vmrun locations probed when
// exec.LookPath("vmrun") fails (e.g. under launchd daemon with minimal PATH).
// Exported so tests can override without patching os.Stat.
var VmrunCandidates = []string{
	"/Applications/VMware Fusion.app/Contents/Public/vmrun",
}

// findVmrun returns the absolute path to the vmrun binary.
// lookPath and stat are injectable for unit tests; production callers pass nil
// to use the real exec.LookPath and os.Stat.
// Returns "" when vmrun cannot be found.
func findVmrun(candidates []string, lookPath func(string) (string, error), stat func(string) error) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if stat == nil {
		stat = func(p string) error { _, err := os.Stat(p); return err }
	}
	if p, err := lookPath("vmrun"); err == nil {
		return p
	}
	for _, p := range candidates {
		if stat(p) == nil {
			return p
		}
	}
	return ""
}

func resolvedVmrunPath() string {
	vmrunOnce.Do(func() {
		vmrunPath = findVmrun(VmrunCandidates, nil, nil)
	})
	return vmrunPath
}

// ResolvedVmrun returns the cached absolute path to the vmrun binary,
// or "" if vmrun was not found. Triggers the sync.Once probe on first call.
func ResolvedVmrun() string { return resolvedVmrunPath() }

// New returns an Enumerator using real system calls.
func New() *Enumerator {
	e := &Enumerator{RunVmrun: vmrunList, Glob: fusionGlob, ReadFile: readFile, RunOrb: orbList, RunVirsh: virshRun}
	if insideOrbGuest() {
		applyOrbGuestGuard(e)
	}
	return e
}

// insideOrbGuest：是否在 OrbStack guest 內（/opt/orbstack-guest 為 orb 注入的 interop 目錄）。
func insideOrbGuest() bool {
	_, err := os.Stat("/opt/orbstack-guest")
	return err == nil
}

// applyOrbGuestGuard：OrbStack guest 的 macOS interop 會讓 guest 看到（且能執行）
// 宿主 mac 的 vmrun/orbctl，導致 guest agent 把宿主的 VM 當成自己的回報、產生重複。
// 在 guest 內停用 vmware/orb 列舉——宿主的 agent 才是這些 VM 的回報來源。
// libvirt 保留：guest 內若真跑 nested KVM，那些 VM 確實屬於它。
func applyOrbGuestGuard(e *Enumerator) {
	e.RunVmrun = func() (string, error) { return "", errors.New("vmware enumeration disabled inside OrbStack guest") }
	e.Glob = func() []string { return nil }
	e.RunOrb = nil
}

func vmrunList() (string, error) {
	p := resolvedVmrunPath()
	if p == "" {
		return "", errors.New("vmrun not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, "list").Output()
	return string(out), err
}

func fusionGlob() []string {
	home, _ := os.UserHomeDir()
	pats := []string{
		filepath.Join(home, "Virtual Machines.localized", "*", "*.vmx"),
		filepath.Join(home, "Virtual Machines", "*", "*.vmx"),
	}
	var out []string
	for _, pat := range pats {
		matches, _ := filepath.Glob(pat)
		out = append(out, matches...)
	}
	return out
}

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}

// Enumerate returns all VMs by merging the vmrun running list and library paths.
// Returns nil (not empty) when VMware is not available at all (vmrun error AND no .vmx paths found).
func (e *Enumerator) Enumerate() ([]VM, error) {
	vmware, err := e.enumerateVMware()
	if err != nil {
		return nil, err
	}
	// OrbStack 機器併入同一清單（RunOrb 可注入；nil 用真實 orbctl）。
	var orb []VM
	if e.RunOrb != nil {
		if out, oerr := e.RunOrb(); oerr == nil {
			orb = parseOrbList(out)
		}
	}
	// libvirt/KVM 機器併入同一清單（RunVirsh 可注入；nil 用真實 virsh）。
	var lv []VM
	if e.RunVirsh != nil {
		lv = enumerateLibvirt(e.RunVirsh)
	}
	if vmware == nil && orb == nil && lv == nil {
		return nil, nil
	}
	return append(append(vmware, orb...), lv...), nil
}

func (e *Enumerator) enumerateVMware() ([]VM, error) {
	runningOut, vmrunErr := e.RunVmrun()
	globPaths := e.Glob()

	if vmrunErr != nil && len(globPaths) == 0 {
		return nil, nil
	}

	// Build running set.
	running := map[string]bool{}
	if vmrunErr == nil {
		for _, line := range strings.Split(strings.TrimSpace(runningOut), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasSuffix(line, ".vmx") {
				running[line] = true
			}
		}
	}

	// Collect unique paths: running + glob.
	seen := map[string]bool{}
	var paths []string
	for p := range running {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}
	for _, p := range globPaths {
		if !seen[p] {
			seen[p] = true
			paths = append(paths, p)
		}
	}

	var vms []VM
	for _, p := range paths {
		content, err := e.ReadFile(p)
		if err != nil {
			continue
		}
		vm := parseVmx(p, content)
		if running[p] {
			vm.State = "running"
		} else {
			vm.State = "stopped"
		}
		vms = append(vms, vm)
	}
	return vms, nil
}

// parseVmx parses key = "value" lines from a .vmx file.
func parseVmx(path, content string) VM {
	vm := VM{VmxPath: path, Name: strings.TrimSuffix(filepath.Base(path), ".vmx")}
	for _, line := range strings.Split(content, "\n") {
		i := strings.Index(line, "=")
		if i < 0 {
			continue
		}
		key := strings.TrimSpace(line[:i])
		val := strings.Trim(strings.TrimSpace(line[i+1:]), `"`)
		switch key {
		case "displayName":
			vm.Name = val
		case "numvcpus":
			vm.VCPU, _ = strconv.Atoi(val)
		case "memsize":
			vm.RamMB, _ = strconv.Atoi(val)
		case "guestOS":
			vm.GuestOS = val
		case "uuid.bios":
			// "56 4d aa bb cc dd ee ff-00 11 22 33 44 55 66 77"
			// Remove spaces but keep the dash separator.
			vm.UUID = strings.ReplaceAll(strings.ReplaceAll(val, " ", ""), "-", "")
			// Reinsert the dash at position 16.
			if len(vm.UUID) == 32 {
				vm.UUID = vm.UUID[:16] + "-" + vm.UUID[16:]
			}
		}
	}
	return vm
}
