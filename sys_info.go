// SPDX-FileCopyrightText: 2022 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("sys_info", newSystemInfo)
}

type systemInfo struct {
	PolledSensor
	entities []string
	values   map[string]string
	msg      string
}

var ents = map[string]struct {
	haName   string
	unameOpt string
	osrName  string
}{
	"machine":        {"Machine", "-m", ""},
	"kernel_name":    {"Kernel name", "-s", ""},
	"kernel_release": {"Kernel release", "-r", ""},
	"kernel_version": {"Kernel version", "-v", ""},
	"os_release":     {"OS release", "", "PRETTY_NAME"},
	"os_name":        {"OS name", "", "NAME"},
	"os_version":     {"OS version", "", "VERSION"},
}

var unameEnts = map[string]string{
	"machine":        "-m",
	"kernel_name":    "-s",
	"kernel_release": "-r",
	"kernel_version": "-v",
}

func newSystemInfo(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("period", "6h")
	defCfg.Set("entities", []string{
		"kernel_release",
		"os_release",
	})
	cfg.Append(defCfg)
	period := cfg.MustGet("period").Duration()
	ee := cfg.MustGet("entities").StringSlice()
	var entities []string
	for _, e := range ee {
		if _, ok := ents[e]; ok {
			entities = append(entities, e)
		}
	}
	sort.Strings(entities)
	si := systemInfo{entities: entities, values: make(map[string]string)}
	si.poller = NewPoller(period, si.Refresh)
	return &si
}

func (s *systemInfo) Config() []EntityConfig {
	var config []EntityConfig
	for _, e := range s.entities {
		cfg := map[string]interface{}{
			"name":           ents[e].haName,
			"state_topic":    "~/sys_info",
			"value_template": fmt.Sprintf("{{value_json.%s}}", e),
			"icon":           "mdi:information-outline",
		}
		config = append(config, EntityConfig{e, "sensor", cfg})
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
	for _, e := range s.entities {
		osrName := ents[e].osrName
		if len(osrName) > 0 {
			if osr == nil {
				osr, _ = osRelease()
			}
			s.values[e] = osr[osrName]
		}
		unameOpt := ents[e].unameOpt
		if len(unameOpt) > 0 {
			cmd := exec.Command("uname", unameOpt)
			if v, err := cmd.Output(); err == nil {
				s.values[e] = strings.TrimSpace(string(v))
			} else {
				delete(s.values, e)
			}
		}
	}
	fields := []string{}
	for _, k := range s.entities {
		fields = append(fields, fmt.Sprintf(`"%s": "%s"`, k, s.values[k]))
	}
	msg := "{" + strings.Join(fields, ", ") + "}"
	if msg != s.msg {
		s.msg = msg
		s.Publish()
	}
}
