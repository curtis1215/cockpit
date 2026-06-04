package inventory

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Machine struct {
	Name       string
	Host       string
	SSHUser    string
	Local      bool
	AgentToken string
}
type Update struct {
	Type, Cmd, Runner, Prompt, Machine, Cwd, Invoke string
}
type Install struct {
	Machine, CurrentCmd string
	Update              Update
	VersionRegex        string
}
type Software struct {
	Name, Kind, LatestSource, Changelog string
	Installs                            []Install
}
type Inventory struct {
	Machines map[string]Machine
	Software []Software
}

func LoadText(b []byte) (Inventory, error) {
	var raw struct {
		Machines map[string]map[string]any `yaml:"machines"`
		Software []struct {
			Name         string `yaml:"name"`
			Kind         string `yaml:"kind"`
			LatestSource string `yaml:"latest_source"`
			Changelog    string `yaml:"changelog"`
			Installs     []struct {
				Machine      string         `yaml:"machine"`
				CurrentCmd   string         `yaml:"current_cmd"`
				VersionRegex string         `yaml:"version_regex"`
				Update       map[string]any `yaml:"update"`
			} `yaml:"installs"`
		} `yaml:"software"`
	}
	if err := yaml.Unmarshal(b, &raw); err != nil {
		return Inventory{}, fmt.Errorf("yaml: %w", err)
	}
	inv := Inventory{Machines: map[string]Machine{}}
	for name, m := range raw.Machines {
		host, _ := m["host"].(string)
		ssh, _ := m["ssh_user"].(string)
		if host == "" || ssh == "" {
			return Inventory{}, fmt.Errorf("machine %s: need host and ssh_user", name)
		}
		local, _ := m["local"].(bool)
		tok, _ := m["agent_token"].(string)
		inv.Machines[name] = Machine{Name: name, Host: host, SSHUser: ssh, Local: local, AgentToken: tok}
	}
	for _, sw := range raw.Software {
		if sw.Name == "" {
			return Inventory{}, fmt.Errorf("software missing name")
		}
		if sw.LatestSource == "" {
			return Inventory{}, fmt.Errorf("software %s: need latest_source", sw.Name)
		}
		kind := sw.Kind
		if kind == "" {
			kind = "custom"
		}
		out := Software{Name: sw.Name, Kind: kind, LatestSource: sw.LatestSource, Changelog: sw.Changelog}
		for i, inst := range sw.Installs {
			// 注意：install.Machine 不再要求存在於 inventory.machines——
			// P3 起機器由 DB（systems）管理，inventory.machines 為 legacy/optional。
			if inst.Machine == "" {
				return Inventory{}, fmt.Errorf("software %s install[%d]: need machine", sw.Name, i)
			}
			if inst.CurrentCmd == "" {
				return Inventory{}, fmt.Errorf("software %s install[%d]: need current_cmd", sw.Name, i)
			}
			up, err := parseUpdate(inst.Update, fmt.Sprintf("software %s install[%d]", sw.Name, i))
			if err != nil {
				return Inventory{}, err
			}
			out.Installs = append(out.Installs, Install{Machine: inst.Machine, CurrentCmd: inst.CurrentCmd, Update: up, VersionRegex: inst.VersionRegex})
		}
		inv.Software = append(inv.Software, out)
	}
	return inv, nil
}

func parseUpdate(raw map[string]any, ctx string) (Update, error) {
	s := func(k string) string { v, _ := raw[k].(string); return v }
	t := s("type")
	switch t {
	case "command":
		if s("cmd") == "" {
			return Update{}, fmt.Errorf("%s: command update needs cmd", ctx)
		}
		return Update{Type: "command", Cmd: s("cmd")}, nil
	case "agent":
		if s("runner") == "" {
			return Update{}, fmt.Errorf("%s: agent update needs runner", ctx)
		}
		if s("prompt") == "" {
			return Update{}, fmt.Errorf("%s: agent update needs prompt", ctx)
		}
		return Update{Type: "agent", Runner: s("runner"), Prompt: s("prompt"), Machine: s("machine"), Cwd: s("cwd"), Invoke: s("invoke")}, nil
	default:
		return Update{}, fmt.Errorf("%s: unknown update.type %q", ctx, t)
	}
}

func Load(path string) (Inventory, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Inventory{}, err
	}
	return LoadText(b)
}

func MachineForToken(inv Inventory, token string) string {
	if token == "" {
		return ""
	}
	for name, m := range inv.Machines {
		if m.AgentToken != "" && m.AgentToken == token {
			return name
		}
	}
	return ""
}

// Marshal serialises inv to YAML bytes that can be loaded back by LoadText.
func Marshal(inv Inventory) ([]byte, error) {
	// Build raw machines map.
	rawMachines := map[string]map[string]any{}
	for name, m := range inv.Machines {
		entry := map[string]any{
			"host":     m.Host,
			"ssh_user": m.SSHUser,
		}
		if m.Local {
			entry["local"] = true
		}
		if m.AgentToken != "" {
			entry["agent_token"] = m.AgentToken
		}
		rawMachines[name] = entry
	}

	// Build raw software list.
	type rawInstall struct {
		Machine      string         `yaml:"machine"`
		CurrentCmd   string         `yaml:"current_cmd"`
		VersionRegex string         `yaml:"version_regex,omitempty"`
		Update       map[string]any `yaml:"update"`
	}
	type rawSoftware struct {
		Name         string       `yaml:"name"`
		Kind         string       `yaml:"kind"`
		LatestSource string       `yaml:"latest_source"`
		Changelog    string       `yaml:"changelog,omitempty"`
		Installs     []rawInstall `yaml:"installs"`
	}

	rawSWList := []rawSoftware{}
	for _, sw := range inv.Software {
		var insts []rawInstall
		for _, inst := range sw.Installs {
			upMap := map[string]any{"type": inst.Update.Type}
			switch inst.Update.Type {
			case "command":
				upMap["cmd"] = inst.Update.Cmd
			case "agent":
				upMap["runner"] = inst.Update.Runner
				upMap["prompt"] = inst.Update.Prompt
				if inst.Update.Machine != "" {
					upMap["machine"] = inst.Update.Machine
				}
				if inst.Update.Cwd != "" {
					upMap["cwd"] = inst.Update.Cwd
				}
				if inst.Update.Invoke != "" {
					upMap["invoke"] = inst.Update.Invoke
				}
			}
			insts = append(insts, rawInstall{
				Machine:      inst.Machine,
				CurrentCmd:   inst.CurrentCmd,
				VersionRegex: inst.VersionRegex,
				Update:       upMap,
			})
		}
		rawSWList = append(rawSWList, rawSoftware{
			Name:         sw.Name,
			Kind:         sw.Kind,
			LatestSource: sw.LatestSource,
			Changelog:    sw.Changelog,
			Installs:     insts,
		})
	}

	doc := map[string]any{
		"machines": rawMachines,
		"software": rawSWList,
	}
	return yaml.Marshal(doc)
}
