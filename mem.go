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
)

func init() {
	RegisterModule("mem", newMem)
}

type MemStats struct {
	mem_used  float32
	swap_used float32
	have_swap bool
}

type Mem struct {
	PolledSensor
	// mem and swap used percent as calced from /proc/meminfo
	stats MemStats
}

func newMem(cfg *config.Config) SyncCloser {
	period := cfg.MustGet("period", config.WithDefaultValue("1m")).Duration()
	stats, err := memStats()
	if err != nil {
		log.Fatalf("unable to read mem stats: %v", err)
	}
	mem := Mem{stats: stats}
	mem.poller = NewPoller(period, mem.Refresh)
	return &mem
}

func memStats() (MemStats, error) {
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
	if stats[0] != 0 && stats[1] != 0 {
		ms.mem_used = float32(((stats[0]-stats[1])*10000)/stats[0]) / 100
	}
	ms.have_swap = stats[2] != 0
	if ms.have_swap {
		ms.swap_used = float32(((stats[2]-stats[3])*10000)/stats[2]) / 100
	}

	return ms, nil
}

func (m *Mem) Config() []EntityConfig {
	var config []EntityConfig
	cfg := map[string]interface{}{
		"name":                "{{.NodeId}} RAM used percent",
		"state_topic":         "~/mem",
		"value_template":      "{{value_json.ram_used_percent}}",
		"unit_of_measurement": "%",
		"icon":                "mdi:gauge",
	}
	config = append(config, EntityConfig{"ram_used_percent", "sensor", cfg})
	if m.stats.have_swap {
		cfg = map[string]interface{}{
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
	stats, err := memStats()
	if err != nil {
		log.Printf("unable to read mem stats: %v", err)
		return
	}

	var changed = forced
	if stats.mem_used != m.stats.mem_used {
		changed = true
		m.stats.mem_used = stats.mem_used
	}
	if m.stats.have_swap && (stats.swap_used != m.stats.swap_used) {
		changed = true
		m.stats.swap_used = stats.swap_used
	}
	if changed {
		msg := fmt.Sprintf(`{"ram_used_percent": %.2f`, stats.mem_used)
		if m.stats.have_swap {
			msg += fmt.Sprintf(`, "swap_used_percent": %.2f`, stats.swap_used)
		}
		msg += "}"
		m.ps.Publish(m.topic, msg)
	}
}
