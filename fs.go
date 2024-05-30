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
	"time"

	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("fs", newMounts)
}

type mounts struct {
	mm []*mount
}

func newMounts(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("period", cfg.MustGet("period", config.WithDefaultValue("10m")).String())
	mm := []*mount{}
	mps := cfg.MustGet("mountpoints").StringSlice()
	for _, name := range mps {
		mCfg := cfg.GetConfig(name)
		mCfg.Append(defCfg)
		mm = append(mm, newMount(name, mCfg))
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
	name    string
	path    string
	mounted bool
	used    uint32
	msg     string
}

type mountConfig struct {
	Period time.Duration
	Path   string
}

func newMount(name string, cfg *config.Config) *mount {
	m := mount{name: name, path: cfg.MustGet("path").String()}
	m.topic = "/" + name
	m.poller = NewPoller(cfg.MustGet("period").Duration(),
		m.Refresh)
	return &m
}

func (m *mount) Config() []EntityConfig {
	var config []EntityConfig
	mtopic := "~/fs" + m.topic
	cfg := map[string]interface{}{
		"name":           "fs " + m.name,
		"state_topic":    mtopic,
		"value_template": "{{value_json.mounted | is_defined}}",
		"device_class":   "connectivity",
		"icon":           "mdi:harddisk",
		"payload_on":     "on",
		"payload_off":    "off",
	}
	config = append(config, EntityConfig{m.name, "binary_sensor", cfg})
	cfg = map[string]interface{}{
		"name":                "fs " + m.name + " used percent",
		"state_topic":         mtopic,
		"value_template":      "{{(value_json.used_percent) | round(2)}}",
		"unit_of_measurement": "%",
		"icon":                "mdi:gauge",
		"availability": []map[string]string{
			{"topic": "~"},
			{"topic": mtopic,
				"value_template":        "{{value_json.mounted | is_defined | default('off')}}",
				"payload_available":     "on",
				"payload_not_available": "off",
			},
		}}
	config = append(config, EntityConfig{m.name + "_used_percent", "sensor", cfg})
	return config
}

func (m *mount) update() bool {
	changed := false
	cmd := exec.Command("df", m.path)
	out, err := cmd.Output()
	mounted := false
	if err == nil {
		r := bufio.NewReader(bytes.NewReader(out))
		r.ReadLine()
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
			used, err := strconv.ParseUint(fields[2], 10, 64)
			if err != nil {
				log.Printf("error parsing uint: %v", err)
				return false
			}
			usedPercent := uint32((used * 10000) / total)
			if usedPercent != m.used {
				m.used = usedPercent
				changed = true
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
	changed := forced
	if m.update() {
		changed = true
	}
	if changed {
		vv := []string{}
		if m.mounted {
			vv = append(vv, `"mounted": "on"`)
			vv = append(vv, fmt.Sprintf(`"used_percent": %.2f`, float32(m.used)/100))
		} else {
			vv = append(vv, `"mounted": "off"`)
		}
		m.msg = fmt.Sprintf("{%s}", strings.Join(vv, ", "))
		m.Publish()
	}
}
