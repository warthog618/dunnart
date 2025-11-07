// SPDX-FileCopyrightText: 2022 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func init() {
	RegisterModule("sys_info", newSystemInfo)
}

type systemInfoConfig struct {
	pollerConfig `yaml:",inline"`
	Entities     []string
}

type systemInfo struct {
	PolledSensor
	entities []string
	msg      string
}

// mapping from entity name to HA display name
var ents = map[string]string{
	"machine":        "Machine",
	"kernel_name":    "Kernel name",
	"kernel_release": "Kernel release",
	"kernel_version": "Kernel version",
	"os_release":     "OS release",
	"os_name":        "OS name",
	"os_version":     "OS version",
	"apt_status":     "APT status",
	"apt_upgradable": "APT upgradable",
	"pacman_status":  "Pacman status",
}

// mapping from entity name to field name in os-release
var osrEnts = map[string]string{
	"os_release": "PRETTY_NAME",
	"os_name":    "NAME",
	"os_version": "VERSION",
}

// mapping from entity to uname option to generate it
var unameEnts = map[string]string{
	"machine":        "-m",
	"kernel_name":    "-s",
	"kernel_release": "-r",
	"kernel_version": "-v",
}

func newSystemInfo(yamlCfg *yaml.Node) SyncCloser {
	cfg := systemInfoConfig{
		pollerConfig: pollerConfig{Period: "6h"},
		Entities:     []string{"kernel_release", "os_release"},
	}
	err := yamlCfg.Decode(&cfg)
	if err != nil {
		log.Fatalf("error reading sysInfo config: %v", err)
	}
	entities := cfg.Entities
	sort.Strings(entities)
	si := systemInfo{entities: entities}
	si.poller = NewPoller(&cfg.pollerConfig, si.Refresh)
	return &si
}

func (s *systemInfo) Config() []EntityConfig {
	var config []EntityConfig
	for _, e := range s.entities {
		cfg := map[string]any{
			"name":           ents[e],
			"state_topic":    "~/sys_info",
			"value_template": fmt.Sprintf("{{value_json.%s}}", e),
		}
		switch e {
		case "apt_upgradable":
			cfg["unit_of_measurement"] = "packages"
			cfg["icon"] = "mdi:package-down"
		case "apt_status", "pacman_status":
			cfg["device_class"] = "update"
			cfg["payload_on"] = "true"
			cfg["payload_off"] = "false"
		default:
			cfg["icon"] = "mdi:information-outline"
		}
		if e == "apt_status" || e == "pacman_status" {
			config = append(config, EntityConfig{e, "binary_sensor", cfg})
		} else {
			config = append(config, EntityConfig{e, "sensor", cfg})
		}
	}

	return config
}

func (s *systemInfo) Publish() {
	s.ps.Publish(s.topic, s.msg)
}

func osRelease() (map[string]string, error) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		kv := strings.SplitN(scanner.Text(), "=", 2)
		if len(kv) == 2 {
			info[kv[0]] = unquote(strings.TrimSpace(kv[1]))
		}
	}
	return info, nil
}

func aptPackagesUpgradable() (int, error) {
	cmd := exec.Command("apt", "-qq", "list", "--upgradable")
	cmd.Stderr = nil
	v, err := cmd.Output()
	if err == nil {
		return strings.Count(string(v), "\n"), nil
	}
	return 0, err
}

func pacmanCheckUpdates() int {
	cmd := exec.Command("checkupdates")
	err := cmd.Run()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
	}
	return 1
}

func unquote(s string) string {
	if len(s) > 0 && s[0] == '"' {
		s = s[1:]
	}
	if len(s) > 0 && s[len(s)-1] == '"' {
		s = s[:len(s)-1]
	}
	return s
}

func (s *systemInfo) Refresh(_ bool) {
	var osr map[string]string
	apu := -1

	fields := []string{}
	for _, e := range s.entities {
		if osrName, ok := osrEnts[e]; ok {
			if osr == nil {
				if r, err := osRelease(); err == nil {
					osr = r
				}
			}
			if osr != nil {
				fields = append(fields, fmt.Sprintf(`"%s": "%s"`, e, osr[osrName]))
			}
			continue
		}
		if unameOpt, ok := unameEnts[e]; ok {
			cmd := exec.Command("uname", unameOpt)
			if v, err := cmd.Output(); err == nil {
				fields = append(fields, fmt.Sprintf(`"%s": "%s"`, e, strings.TrimSpace(string(v))))
			}
			continue
		}
		if e == "pacman_status" {
			checkupdates := pacmanCheckUpdates()
			if checkupdates == 2 {
				fields = append(fields, fmt.Sprintf(`"%s": "false"`, e))
			} else {
				fields = append(fields, fmt.Sprintf(`"%s": "true"`, e))
			}
			continue
		}
		// must be an apt status
		if apu == -1 {
			if c, err := aptPackagesUpgradable(); err == nil {
				apu = c
			}
		}
		if e == "apt_status" {
			if apu == 0 {
				fields = append(fields, fmt.Sprintf(`"%s": "false"`, e))
			} else if apu > 0 {
				fields = append(fields, fmt.Sprintf(`"%s": "true"`, e))
			}
			continue
		}
		if e == "apt_upgradable" {
			if apu != -1 {
				fields = append(fields, fmt.Sprintf(`"%s": "%d"`, e, apu))
			}
		}
	}
	msg := "{" + strings.Join(fields, ", ") + "}"
	if msg != s.msg {
		s.msg = msg
		s.Publish()
	}
}
