// Copyright © 2022 Kent Gibson <warthog618@gmail.com>.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

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
	entities []string
	// mem and swap used percent as calced from /proc/meminfo
	stats MemStats
}

func newMem(cfg *config.Config) SyncCloser {
	defCfg := dict.New(dict.WithMap(map[string]interface{}{
		"period": "1m",
		"entities": []string{
			"ram_used_percent",
			"swap_used_percent",
		},
	}))
	cfg.Append(defCfg)
	entities := cfg.MustGet("entities").StringSlice()
	period := cfg.MustGet("period").Duration()
	stats, err := memStats(entities)
	if err != nil {
		log.Fatalf("unable to read mem stats: %v", err)
	}
	mem := Mem{entities: entities, stats: stats}
	mem.poller = NewPoller(period, mem.Refresh)
	return &mem
}

func memStats(fields []string) (MemStats, error) {
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
	for _, field := range fields {
		if field == "ram_used_percent" && stats[0] != 0 && stats[1] != 0 {
			ms["ram_used_percent"] = float32(((stats[0]-stats[1])*10000)/stats[0]) / 100
		}
		if field == "swap_used_percent" && stats[2] != 0 {
			ms["swap_used_percent"] = float32(((stats[2]-stats[3])*10000)/stats[2]) / 100
		}
	}

	return ms, nil
}

func (m *Mem) Config() []EntityConfig {
	var config []EntityConfig
	if _, ok := m.stats["ram_used_percent"]; ok {
		cfg := map[string]interface{}{
			"name":                "{{.NodeId}} RAM used percent",
			"state_topic":         "~/mem",
			"value_template":      "{{value_json.ram_used_percent}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
		}
		config = append(config, EntityConfig{"ram_used_percent", "sensor", cfg})
	}
	if _, ok := m.stats["swap_used_percent"]; ok {
		cfg := map[string]interface{}{
			"name":                "{{.NodeId}} swap used percent",
			"state_topic":         "~/mem",
			"value_template":      "{{value_json.swap_used_percent}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
		}
		config = append(config, EntityConfig{"swap_used_percent", "sensor", cfg})
	}
	return config
}

func (m *Mem) Refresh(forced bool) {
	stats, err := memStats(m.entities)
	if err != nil {
		log.Printf("unable to read mem stats: %v", err)
		return
	}

	var changed = forced
	for k := range m.stats {
		if stats[k] != m.stats[k] {
			changed = true
			m.stats[k] = stats[k]
		}
	}
	if changed {
		fields := []string{}
		for k, v := range m.stats {
			fields = append(fields, fmt.Sprintf(`"%s": %.2f`, k, v))
		}
		msg := "{" + strings.Join(fields, ", ") + "}"
		m.ps.Publish(m.topic, msg)
	}
}
