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

	"github.com/pkg/errors"
	"github.com/warthog618/config"
)

func init() {
	RegisterModule("cpu", newCPU)
}

type CPU struct {
	PolledSensor
	// as read from /proc/stat
	stats     CpuStats
	temp      uint64
	have_temp bool
}

func newCPU(cfg *config.Config) SyncCloser {
	period := cfg.MustGet("period", config.WithDefaultValue("1m")).Duration()
	have_temp := false
	temp, err := cpuTemp()
	if err == nil {
		have_temp = true
	}
	stats, err := cpuStats()
	if err != nil {
		log.Fatalf("unable to read cpu stats: %v", err)
	}
	cpu := CPU{stats: stats, temp: temp, have_temp: have_temp}
	cpu.poller = NewPoller(period, cpu.Refresh)
	return &cpu
}

func (c *CPU) Config() []EntityConfig {
	var config []EntityConfig
	cfg := map[string]interface{}{
		"name":                "{{.NodeId}} CPU used percent",
		"state_topic":         "~/cpu",
		"value_template":      "{{(100 - value_json.idle_percent) | round(2)}}",
		"unit_of_measurement": "%",
		"icon":                "mdi:gauge",
	}
	config = append(config, EntityConfig{"used_percent", "sensor", cfg})
	if c.have_temp {
		cfg = map[string]interface{}{
			"name":                "{{.NodeId}} CPU temperature",
			"state_topic":         "~/cpu",
			"value_template":      "{{value_json.temperature}}",
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

func cpuTemp() (uint64, error) {
	f, err := os.Open("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, scanner.Err()
	}
	return strconv.ParseUint(scanner.Text(), 10, 64)
}

func (c *CPU) Refresh(forced bool) {

	idle_percent := float32(0)
	changed := forced
	temp, err := cpuTemp()
	if err == nil {
		if temp != c.temp {
			changed = true
			c.temp = temp
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
		idle_percent = float32((d[3]*10000)/total) / 100
		if stats[3] != c.stats[3] {
			changed = true
		}
	}
	if changed {
		value := fmt.Sprintf(`{"idle_percent": %.2f`, idle_percent)
		if c.have_temp {
			value += fmt.Sprintf(`, "temperature": %.2f`, float32(temp)/1000)
		}
		value += "}"
		c.ps.Publish(c.topic, value)
	}
	c.stats = stats
}

func delta(old, new uint64) uint64 {
	if new <= old {
		return 0
	}
	return new - old
}
