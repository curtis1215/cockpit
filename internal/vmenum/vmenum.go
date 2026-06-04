package vmenum

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
}

// New returns an Enumerator using real system calls.
func New() *Enumerator {
	return &Enumerator{RunVmrun: vmrunList, Glob: fusionGlob, ReadFile: readFile}
}

func vmrunList() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vmrun", "list").Output()
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
	vm := VM{VmxPath: path}
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
