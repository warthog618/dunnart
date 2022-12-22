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

	"github.com/pkg/errors"
	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("cpu", newCPU)
}

type CPU struct {
	PolledSensor
	entities map[string]bool
	// as read from /proc/stat
	stats        CpuStats
	tfile        string
	temp         int64
	have_temp    bool
	idle_percent float32
	msg          string
}

func newCPU(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("period", "1m")
	defCfg.Set("entities", []string{
		"temperature",
		"used_percent",
	})
	defCfg.Set("temperature.file", "/sys/class/thermal/thermal_zone0/temp")
	cfg.Append(defCfg)
	period := cfg.MustGet("period").Duration()
	entities := map[string]bool{}
	for _, e := range cfg.MustGet("entities").StringSlice() {
		entities[e] = true
	}
	stats, err := cpuStats()
	if err != nil {
		log.Fatalf("unable to read cpu stats: %v", err)
	}
	cpu := CPU{entities: entities, stats: stats}
	if entities["temperature"] {
		tfile := cfg.MustGet("temperature.file").String()
		temp, err := cpuTemp(tfile)
		if err == nil {
			cpu.temp = temp
		}
		cpu.tfile = tfile
	}
	cpu.poller = NewPoller(period, cpu.Refresh)
	return &cpu
}

func (c *CPU) Config() []EntityConfig {
	var config []EntityConfig
	if c.entities["used_percent"] {
		cfg := map[string]interface{}{
			"name":                "{{.NodeId}} CPU used percent",
			"state_topic":         "~/cpu",
			"value_template":      "{{(100 - value_json.idle_percent) | round(2)}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
		}
		config = append(config, EntityConfig{"used_percent", "sensor", cfg})
	}
	if c.entities["temperature"] {
		cfg := map[string]interface{}{
			"name":                "{{.NodeId}} CPU temperature",
			"state_topic":         "~/cpu",
			"value_template":      "{{value_json.temperature | round(2) }}",
			"device_class":        "temperature",
			"unit_of_measurement": "°C",
		}
		config = append(config, EntityConfig{"temperature", "sensor", cfg})
	}
	return config
}

// entries are [user, nicer, system, idle, iowait, irq, softirq, steal, quest, guest_nice]
type CpuStats [10]uint64

func cpuStats() (CpuStats, error) {
	var stats CpuStats
	f, err := os.Open("/proc/stat")
	if err != nil {
		return stats, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return stats, scanner.Err()
	}
	fields := strings.Fields(scanner.Text())
	num_fields := len(fields)
	if fields[0] != "cpu" || num_fields < 8 {
		return stats, errors.Errorf("bad cpu line: %v", scanner.Text())
	}
	num_stats := num_fields - 1
	if num_stats > len(stats) {
		num_stats = len(stats)
	}
	for i := 0; i < num_stats; i++ {
		v, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return stats, err
		}
		stats[i] = v
	}
	return stats, nil
}

func cpuTemp(tfile string) (int64, error) {
	f, err := os.Open(tfile)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, scanner.Err()
	}
	return strconv.ParseInt(scanner.Text(), 10, 64)
}

func (c *CPU) Publish() {
	c.ps.Publish(c.topic, c.msg)
}

func (c *CPU) Refresh(forced bool) {
	changed := forced
	temp, err := cpuTemp(c.tfile)
	if err == nil {
		if temp != c.temp {
			changed = true
			c.temp = temp
			c.have_temp = true
		}
	}
	stats, err := cpuStats()
	if err != nil {
		log.Printf("unable to read cpu stats: %v", err)
		return
	}
	d := CpuStats{}
	total := uint64(0)
	for i := 0; i < len(d); i++ {
		d[i] = delta(c.stats[i], stats[i])
		total += d[i]
	}
	if total != 0 {
		idle_percent := float32((d[3]*10000)/total) / 100
		if c.idle_percent != idle_percent {
			changed = true
			c.idle_percent = idle_percent
		}
	}
	if changed {
		fields := []string{}
		if c.entities["used_percent"] {
			fields = append(fields, fmt.Sprintf(`"idle_percent": %.2f`, c.idle_percent))
		}
		if c.have_temp {
			fields = append(fields, fmt.Sprintf(`"temperature": %.2f`, float32(c.temp)/1000))
		}
		c.msg = "{" + strings.Join(fields, ", ") + "}"
		c.Publish()
	}
	c.stats = stats
}

func delta(old, new uint64) uint64 {
	if new <= old {
		return 0
	}
	return new - old
}
