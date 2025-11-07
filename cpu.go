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
	"gopkg.in/yaml.v3"
)

func init() {
	RegisterModule("cpu", newCPU)
}

type cpu struct {
	PolledSensor
	entities map[string]bool
	// as read from /proc/stat
	stats       CPUStats
	tpath       string
	temp        int64
	haveTemp    bool
	idlePercent float32
	uptime      float64
	msg         string
}

type cpuTemperatureConfig struct {
	Path string
}

type cpuConfig struct {
	pollerConfig `yaml:",inline"`
	Entities     []string
	Temperature  cpuTemperatureConfig
}

func newCPU(yamlCfg *yaml.Node) SyncCloser {
	cfg := cpuConfig{
		pollerConfig: pollerConfig{Period: "1m"},
		Entities:     []string{"temperature", "used_percent"},
		Temperature:  cpuTemperatureConfig{Path: "/sys/class/thermal/thermal_zone0/temp"},
	}
	err := yamlCfg.Decode(&cfg)
	if err != nil {
		log.Fatalf("error reading cpu config: %v", err)
	}
	entities := map[string]bool{}
	for _, e := range cfg.Entities {
		entities[e] = true
	}
	stats, err := cpuStats()
	if err != nil {
		log.Fatalf("unable to read cpu stats: %v", err)
	}
	cpu := cpu{entities: entities, stats: stats}
	if entities["temperature"] {
		tpath := cfg.Temperature.Path
		temp, err := cpuTemp(tpath)
		if err == nil {
			cpu.temp = temp
		}
		cpu.tpath = tpath
	}
	cpu.poller = NewPoller(&cfg.pollerConfig, cpu.Refresh)
	return &cpu
}

func (c *cpu) Config() []EntityConfig {
	var config []EntityConfig
	if c.entities["used_percent"] {
		cfg := map[string]any{
			"name":                "CPU used percent",
			"state_topic":         "~/cpu",
			"value_template":      "{{(100 - value_json.idle_percent) | round(2)}}",
			"unit_of_measurement": "%",
			"icon":                "mdi:gauge",
		}
		config = append(config, EntityConfig{"used_percent", "sensor", cfg})
	}
	if c.entities["temperature"] {
		cfg := map[string]any{
			"name":                "CPU temperature",
			"state_topic":         "~/cpu",
			"value_template":      "{{value_json.temperature | round(2) }}",
			"device_class":        "temperature",
			"unit_of_measurement": "°C",
		}
		config = append(config, EntityConfig{"temperature", "sensor", cfg})
	}
	if c.entities["uptime"] {
		cfg := map[string]any{
			"name":                "Uptime",
			"state_topic":         "~/cpu",
			"value_template":      "{{value_json.uptime | int }}",
			"device_class":        "duration",
			"unit_of_measurement": "s",
		}
		config = append(config, EntityConfig{"uptime", "sensor", cfg})
	}
	return config
}

// CPUStats is an array of stats read from /proc/stat.
// Entries are [user, nicer, system, idle, iowait, irq, softirq, steal, quest, guest_nice]
type CPUStats [10]uint64

func cpuStats() (CPUStats, error) {
	var stats CPUStats
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
	numFields := len(fields)
	if fields[0] != "cpu" || numFields < 8 {
		return stats, errors.Errorf("bad cpu line: %v", scanner.Text())
	}
	numStats := min(numFields-1, len(stats))
	for i := range numStats {
		v, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return stats, err
		}
		stats[i] = v
	}
	return stats, nil
}

func cpuTemp(tpath string) (int64, error) {
	f, err := os.Open(tpath)
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

func (c *cpu) Publish() {
	c.ps.Publish(c.topic, c.msg)
}

func uptime() (float64, error) {
	f, err := os.Open("/proc/uptime")
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		return 0, scanner.Err()
	}
	return strconv.ParseFloat(strings.Fields(scanner.Text())[0], 32)
}

func (c *cpu) Refresh(forced bool) {
	changed := forced
	if c.entities["uptime"] {
		if uptime, err := uptime(); err == nil {
			c.uptime = uptime
			changed = true
		}
	}
	temp, err := cpuTemp(c.tpath)
	if err == nil {
		if temp != c.temp {
			changed = true
			c.temp = temp
			c.haveTemp = true
		}
	}
	stats, err := cpuStats()
	if err != nil {
		log.Printf("unable to read cpu stats: %v", err)
		return
	}
	d := CPUStats{}
	total := uint64(0)
	for i := range len(d) {
		d[i] = delta(c.stats[i], stats[i])
		total += d[i]
	}
	if total != 0 {
		idlePercent := float32((d[3]*10000)/total) / 100
		if c.idlePercent != idlePercent {
			changed = true
			c.idlePercent = idlePercent
		}
	}
	if changed {
		fields := []string{}
		if c.entities["used_percent"] {
			fields = append(fields, fmt.Sprintf(`"idle_percent": %.2f`, c.idlePercent))
		}
		if c.haveTemp {
			fields = append(fields, fmt.Sprintf(`"temperature": %.2f`, float32(c.temp)/1000))
		}
		if c.entities["uptime"] {
			fields = append(fields, fmt.Sprintf(`"uptime": %.2f`, c.uptime))
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
