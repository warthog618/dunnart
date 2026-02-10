// SPDX-FileCopyrightText: 2022 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func init() {
	RegisterModule("fs", newMounts)
}

type mounts struct {
	mm []*mount
}

type fsMountPointConfig struct {
	pollerConfig `yaml:",inline"`
	Path         string
	Entities     []string
}

type fsConfig struct {
	pollerConfig `yaml:",inline"`
	Mountpoints  []string
	Entities     []string
}

func newMounts(yamlCfg *yaml.Node) SyncCloser {
	cfg := fsConfig{
		pollerConfig: pollerConfig{Period: "10m"},
		Entities: []string{
			"used_percent",
		},
	}
	// structured for fsConfig
	err := yamlCfg.Decode(&cfg)
	if err != nil {
		log.Fatalf("error reading fs config: %v", err)
	}
	// unstructured for mountpoint config
	mpCfg := make(map[string]yaml.Node)
	err = yamlCfg.Decode(&mpCfg)
	if err != nil {
		log.Fatalf("error parsing fs mp config: %v", err)
	}

	// mountpoints may inherit period and entities
	mm := []*mount{}
	for _, name := range cfg.Mountpoints {
		mCfg := fsMountPointConfig{
			pollerConfig: cfg.pollerConfig,
			Entities:     cfg.Entities,
		}
		yCfg := mpCfg[name]
		err := yCfg.Decode(&mCfg)
		if err != nil {
			log.Fatalf("error reading fs %s config: %v", name, err)
		}
		mm = append(mm, newMount(name, &mCfg))
	}
	return &mounts{mm: mm}
}

func (m *mounts) Config() []EntityConfig {
	var config []EntityConfig
	for _, mount := range m.mm {
		config = append(config, mount.Config()...)
	}
	return config
}

func (m *mounts) Publish() {
	for _, mount := range m.mm {
		mount.Publish()
	}
}

func (m *mounts) Sync(ps PubSub) {
	for _, mount := range m.mm {
		mount.Sync(ps)
	}
}

func (m *mounts) Close() {
}

type mount struct {
	PolledSensor
	entities    map[string]bool
	name        string
	path        string
	mounted     bool
	totalBytes  uint64
	usedBytes   uint64
	usedPercent uint32
	msg         string
	cfg         []EntityConfig
}

func newMount(name string, cfg *fsMountPointConfig) *mount {
	m := mount{name: name, path: cfg.Path, entities: map[string]bool{}}
	m.topic = "/" + name
	m.poller = NewPoller(&cfg.pollerConfig, m.Refresh)
	mtopic := "~/fs" + m.topic
	ecfg := map[string]any{
		"name":           "fs " + m.name,
		"state_topic":    mtopic,
		"value_template": "{{value_json.mounted | is_defined}}",
		"device_class":   "connectivity",
		"icon":           "mdi:harddisk",
		"payload_on":     "on",
		"payload_off":    "off",
	}
	m.cfg = append(m.cfg, EntityConfig{m.name, "binary_sensor", ecfg})
	for _, e := range cfg.Entities {
		m.entities[e] = true
	}

	availability_cfg := []map[string]string{
		{"topic": "~"},
		{"topic": mtopic,
			"value_template":        "{{value_json.mounted | is_defined | default('off')}}",
			"payload_available":     "on",
			"payload_not_available": "off",
		}}
	if m.entities["used_percent"] {
		ecfg = map[string]any{
			"name":                "fs " + m.name + " used percent",
			"state_topic":         mtopic,
			"value_template":      "{{(value_json.used_percent) | round(2)}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
			"availability":        availability_cfg,
		}
		m.cfg = append(m.cfg, EntityConfig{m.name + "_used_percent", "sensor", ecfg})
	}
	if m.entities["total_bytes"] {
		ecfg = map[string]any{
			"name":           "fs " + m.name + " total bytes",
			"state_topic":    mtopic,
			"value_template": "{{(value_json.total_bytes) | is_defined}}",
			"icon":           "mdi:harddisk",
			"availability":   availability_cfg,
		}
		m.cfg = append(m.cfg, EntityConfig{m.name + "_total_bytes", "sensor", ecfg})
	}
	if m.entities["used_bytes"] {
		ecfg = map[string]any{
			"name":           "fs " + m.name + " used bytes",
			"state_topic":    mtopic,
			"value_template": "{{(value_json.used_bytes) | is_defined}}",
			"icon":           "mdi:gauge",
			"availability":   availability_cfg,
		}
		m.cfg = append(m.cfg, EntityConfig{m.name + "_used_bytes", "sensor", ecfg})
	}
	return &m
}

func (m *mount) Config() []EntityConfig {
	return m.cfg
}

func (m *mount) update() bool {
	changed := false
	cmd := exec.Command("df", "-B1", m.path)
	out, err := cmd.Output()
	mounted := false
	if err == nil {
		r := bufio.NewReader(bytes.NewReader(out))
		_, _, _ = r.ReadLine()
		line, _, err := r.ReadLine()
		if err != nil {
			log.Printf("error parsing df: %v", err)
			return false
		}
		// split line on whitespace
		fields := strings.Fields(string(line))
		if len(fields) >= 6 && fields[5] == m.path {
			mounted = true
			total, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				log.Printf("error parsing uint: %v", err)
				return false
			}
			if m.entities["total_bytes"] {
				if total != m.totalBytes {
					m.totalBytes = total
					changed = true
				}
			}
			used, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				log.Printf("error parsing uint: %v", err)
				return false
			}
			if m.entities["used_bytes"] {
				if used != m.usedBytes {
					m.usedBytes = used
					changed = true
				}
			}
			if m.entities["used_percent"] {
				usedPercent := uint32((used * 10000) / total)
				if usedPercent != m.usedPercent {
					m.usedPercent = usedPercent
					changed = true
				}
			}
		}
	}
	if m.mounted != mounted {
		changed = true
		m.mounted = mounted
	}
	return changed
}

func (m *mount) Publish() {
	m.ps.Publish(m.topic, m.msg)
}

func (m *mount) Refresh(forced bool) {
	if !m.update() && !forced {
		return
	}
	vv := []string{}
	if m.mounted {
		vv = append(vv, `"mounted": "on"`)
		if m.entities["total_bytes"] {
			vv = append(vv, fmt.Sprintf(`"total_bytes": %d`, m.totalBytes))
		}
		if m.entities["used_bytes"] {
			vv = append(vv, fmt.Sprintf(`"used_bytes": %d`, m.usedBytes))
		}
		if m.entities["used_percent"] {
			vv = append(vv, fmt.Sprintf(`"used_percent": %.2f`, float32(m.usedPercent)/100))
		}
	} else {
		vv = append(vv, `"mounted": "off"`)
	}
	m.msg = fmt.Sprintf("{%s}", strings.Join(vv, ", "))
	m.Publish()

}
