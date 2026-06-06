package vmenum

import (
	"errors"
	"testing"
)

const vmx = `
.encoding = "UTF-8"
displayName = "ubuntu-vm"
numvcpus = "4"
memsize = "4096"
guestOS = "ubuntu-64"
uuid.bios = "56 4d aa bb cc dd ee ff-00 11 22 33 44 55 66 77"
`

func TestParseVmx(t *testing.T) {
	vm := parseVmx("/p/ubuntu-vm.vmx", vmx)
	if vm.Name != "ubuntu-vm" || vm.VCPU != 4 || vm.RamMB != 4096 || vm.GuestOS != "ubuntu-64" {
		t.Fatalf("vmx: %+v", vm)
	}
	if vm.UUID != "564daabbccddeeff-0011223344556677" {
		t.Fatalf("uuid: %q", vm.UUID)
	}
}

func TestParseVmxNameFallback(t *testing.T) {
	vm := parseVmx("/p/noname.vmx", `memsize = "1024"`)
	if vm.Name != "noname" {
		t.Fatalf("name fallback: %q", vm.Name)
	}
	if vm.RamMB != 1024 {
		t.Fatalf("ram: %d", vm.RamMB)
	}
}

const vmrunOut = `Total running VMs: 2
/Users/alice/Virtual Machines.localized/ubuntu-vm/ubuntu-vm.vmx
/Users/alice/Virtual Machines.localized/win11/win11.vmx
`

func TestEnumerate(t *testing.T) {
	readFn := func(p string) (string, error) { return vmx, nil }

	e := &Enumerator{
		RunVmrun: func() (string, error) { return vmrunOut, nil },
		Glob:     func() []string { return []string{"/p/ubuntu-vm.vmx", "/p/win11.vmx"} },
		ReadFile: readFn,
	}
	vms, err := e.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	// union: 2 from vmrun + 2 from glob, but only unique paths → 4 total
	// (paths differ between vmrun and glob fakes)
	if len(vms) != 4 {
		t.Fatalf("n=%d: %+v", len(vms), vms)
	}
	// all running ones have state=running
	for _, vm := range vms {
		if vm.VmxPath == "/Users/alice/Virtual Machines.localized/ubuntu-vm/ubuntu-vm.vmx" ||
			vm.VmxPath == "/Users/alice/Virtual Machines.localized/win11/win11.vmx" {
			if vm.State != "running" {
				t.Fatalf("state: %+v", vm)
			}
		}
	}
}

func TestEnumerateNoVMware(t *testing.T) {
	e := &Enumerator{
		RunVmrun: func() (string, error) { return "", errNo{} },
		Glob:     func() []string { return nil },
		ReadFile: func(p string) (string, error) { return "", nil },
	}
	vms, err := e.Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	if vms != nil {
		t.Fatalf("no vmware → nil, got %+v", vms)
	}
}

type errNo struct{}

func (errNo) Error() string { return "vmrun not found" }

// --- findVmrun unit tests ---

// lookPathMiss is a LookPath stub that always returns "not found".
func lookPathMiss(name string) (string, error) { return "", errors.New("not found") }

func statHit(hit string) func(string) error {
	return func(p string) error {
		if p == hit {
			return nil
		}
		return errors.New("not found")
	}
}

func statNone(p string) error { return errors.New("not found") }

func TestFindVmrunUsesFirstCandidate(t *testing.T) {
	candidates := []string{"/a/vmrun", "/b/vmrun"}
	got := findVmrun(candidates, lookPathMiss, statHit("/a/vmrun"))
	if got != "/a/vmrun" {
		t.Fatalf("expected /a/vmrun, got %q", got)
	}
}

func TestFindVmrunFallsToSecondCandidate(t *testing.T) {
	candidates := []string{"/a/vmrun", "/b/vmrun"}
	got := findVmrun(candidates, lookPathMiss, statHit("/b/vmrun"))
	if got != "/b/vmrun" {
		t.Fatalf("expected /b/vmrun, got %q", got)
	}
}

func TestFindVmrunReturnsEmptyWhenNoneFound(t *testing.T) {
	candidates := []string{"/a/vmrun", "/b/vmrun"}
	got := findVmrun(candidates, lookPathMiss, statNone)
	if got != "" {
		t.Fatalf("expected empty string when not found, got %q", got)
	}
}

func TestEnumerateOrbStack(t *testing.T) {
	e := &Enumerator{
		RunVmrun: func() (string, error) { return "", errNo{} },
		Glob:     func() []string { return nil },
		ReadFile: func(string) (string, error) { return "", errNo{} },
		RunOrb: func() (string, error) {
			return `[{"id":"01KT7A20P7","name":"test-runner","state":"running","image":{"distro":"ubuntu","version":"noble","arch":"arm64"}},{"id":"01XX","name":"old-box","state":"stopped","image":{"distro":"debian","version":"","arch":"arm64"}}]`, nil
		},
	}
	vms, err := e.Enumerate()
	if err != nil || len(vms) != 2 {
		t.Fatalf("orb enumerate: %v %+v", err, vms)
	}
	if vms[0].Name != "test-runner" || vms[0].State != "running" || vms[0].UUID != "orb-01KT7A20P7" || vms[0].GuestOS != "ubuntu-noble" {
		t.Fatalf("vm0: %+v", vms[0])
	}
	if vms[1].State != "stopped" || vms[1].GuestOS != "debian" {
		t.Fatalf("vm1: %+v", vms[1])
	}
}

func TestEnumerateBothSources(t *testing.T) {
	e := &Enumerator{
		RunVmrun: func() (string, error) { return "Total running VMs: 1\n/p/a.vmx\n", nil },
		Glob:     func() []string { return []string{"/p/a.vmx"} },
		ReadFile: func(string) (string, error) { return `displayName = "vmw-a"` + "\n", nil },
		RunOrb: func() (string, error) {
			return `[{"id":"01A","name":"orb-a","state":"running","image":{"distro":"ubuntu","version":"x","arch":"arm64"}}]`, nil
		},
	}
	vms, _ := e.Enumerate()
	if len(vms) != 2 {
		t.Fatalf("both: %+v", vms)
	}
}

func TestEnumerateLibvirt(t *testing.T) {
	dominfo := map[string]string{
		"multica-sitruc": `Id:             1
Name:           multica-sitruc
UUID:           f3a4eea3-65fb-4483-b28a-e4dbfc2f8330
OS Type:        hvm
State:          running
CPU(s):         4
CPU time:       12345.6s
Max memory:     16777216 KiB
Used memory:    16777216 KiB
Persistent:     yes
Autostart:      enable
`,
		"old-vm": `Id:             -
Name:           old-vm
UUID:           11111111-2222-3333-4444-555555555555
OS Type:        hvm
State:          shut off
CPU(s):         2
Max memory:     4194304 KiB
Persistent:     yes
`,
	}
	e := &Enumerator{
		RunVmrun: func() (string, error) { return "", errNo{} },
		Glob:     func() []string { return nil },
		ReadFile: func(string) (string, error) { return "", errNo{} },
		RunVirsh: func(args ...string) (string, error) {
			if args[0] == "list" {
				return "multica-sitruc\nold-vm\n\n", nil
			}
			if args[0] == "dominfo" {
				return dominfo[args[1]], nil
			}
			t.Fatalf("unexpected virsh args: %v", args)
			return "", nil
		},
	}
	vms, err := e.Enumerate()
	if err != nil || len(vms) != 2 {
		t.Fatalf("libvirt enumerate: %v %+v", err, vms)
	}
	v0 := vms[0]
	if v0.Name != "multica-sitruc" || v0.State != "running" ||
		v0.UUID != "f3a4eea3-65fb-4483-b28a-e4dbfc2f8330" || v0.VCPU != 4 || v0.RamMB != 16384 {
		t.Fatalf("vm0: %+v", v0)
	}
	if vms[1].State != "stopped" || vms[1].RamMB != 4096 {
		t.Fatalf("vm1: %+v", vms[1])
	}
}

func TestEnumerateLibvirtUnavailable(t *testing.T) {
	e := &Enumerator{
		RunVmrun: func() (string, error) { return "", errNo{} },
		Glob:     func() []string { return nil },
		ReadFile: func(string) (string, error) { return "", errNo{} },
		RunVirsh: func(args ...string) (string, error) { return "", errNo{} },
	}
	vms, err := e.Enumerate()
	if err != nil || vms != nil {
		t.Fatalf("expected nil,nil when no hypervisor: %v %+v", err, vms)
	}
}

func TestFindVirshFallbackPath(t *testing.T) {
	got := findVirsh(lookPathMiss, func(p string) error {
		if p == "/usr/bin/virsh" {
			return nil
		}
		return errNo{}
	})
	if got != "/usr/bin/virsh" {
		t.Fatalf("findVirsh = %q", got)
	}
}
