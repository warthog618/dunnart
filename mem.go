// SPDX-FileCopyrightText: 2022 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("mem", newMem)
}

type MemStats map[string]float32

type Mem struct {
	PolledSensor
	entities map[string]bool
	// mem and swap used percent as calced from /proc/meminfo
	stats MemStats
	msg   string
}

func newMem(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("period", "1m")
	defCfg.Set("entities", []string{
		"ram_used_percent",
		"swap_used_percent",
	})
	cfg.Append(defCfg)
	entities := map[string]bool{}
	for _, e := range cfg.MustGet("entities").StringSlice() {
		entities[e] = true
	}
	period := cfg.MustGet("period").Duration()
	stats, err := memStats(entities)
	if err != nil {
		log.Fatalf("unable to read mem stats: %v", err)
	}
	mem := Mem{entities: entities, stats: stats}
	mem.poller = NewPoller(period, mem.Refresh)
	return &mem
}

func memStats(fields map[string]bool) (MemStats, error) {
	names := []string{"MemTotal:", "MemAvailable:", "SwapTotal:", "SwapFree:"}
	stats := [4]uint64{}
	ms := MemStats{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return ms, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		for i := 0; i < len(stats); i++ {
			if strings.HasPrefix(line, names[i]) {
				fields := strings.Fields(line)
				v, err := strconv.ParseUint(fields[1], 10, 64)
				if err != nil {
					continue
				}
				stats[i] = v
			}
		}
	}
	if fields["ram_used_percent"] && stats[0] != 0 && stats[1] != 0 {
		ms["ram_used_percent"] = float32(((stats[0]-stats[1])*10000)/stats[0]) / 100
	}
	if fields["swap_used_percent"] && stats[2] != 0 {
		ms["swap_used_percent"] = float32(((stats[2]-stats[3])*10000)/stats[2]) / 100
	}

	return ms, nil
}

func (m *Mem) Config() []EntityConfig {
	var config []EntityConfig
	if m.entities["ram_used_percent"] {
		cfg := map[string]interface{}{
			"name":                "RAM used percent",
			"state_topic":         "~/mem",
			"value_template":      "{{value_json.ram_used_percent | is_defined}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
		}
		config = append(config, EntityConfig{"ram_used_percent", "sensor", cfg})
	}
	if m.entities["swap_used_percent"] {
		cfg := map[string]interface{}{
			"name":                "swap used percent",
			"state_topic":         "~/mem",
			"value_template":      "{{value_json.swap_used_percent | is_defined}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
		}
		config = append(config, EntityConfig{"swap_used_percent", "sensor", cfg})
	}
	return config
}

func (m *Mem) Publish() {
	m.ps.Publish(m.topic, m.msg)
}

func (m *Mem) Refresh(forced bool) {
	stats, err := memStats(m.entities)
	if err != nil {
		log.Printf("unable to read mem stats: %v", err)
		return
	}

	var changed = forced
	for k := range stats {
		if stats[k] != m.stats[k] {
			changed = true
		}
	}
	m.stats = stats
	if changed {
		fields := []string{}
		for k, v := range m.stats {
			fields = append(fields, fmt.Sprintf(`"%s": %.2f`, k, v))
		}
		m.msg = "{" + strings.Join(fields, ", ") + "}"
		m.Publish()
	}
}
