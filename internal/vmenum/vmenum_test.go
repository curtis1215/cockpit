package vmenum

import (
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
