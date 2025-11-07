// SPDX-FileCopyrightText: 2022 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func init() {
	RegisterModule("net", newNets)
}

type nets struct {
	nn []*netIf
}

type netConfig struct {
	pollerConfig `yaml:",inline"`
	Entities     []string
	Interfaces   []string
}

type netIfConfig struct {
	pollerConfig `yaml:",inline"`
	Entities     []string
	Link         pollerConfig
	Stats        pollerConfig
}

func newNets(yamlCfg *yaml.Node) SyncCloser {
	cfg := netConfig{
		pollerConfig: pollerConfig{Period: "1m"},
		Entities: []string{
			"operstate",
			"rx_bytes",
			"tx_bytes",
			"rx_throughput",
			"tx_throughput",
		},
	}
	// structured for netConfig
	err := yamlCfg.Decode(&cfg)
	if err != nil {
		log.Fatalf("error reading net config: %v", err)
	}
	// unstructured for interface config
	ifCfg := make(map[string]yaml.Node)
	err = yamlCfg.Decode(&ifCfg)
	if err != nil {
		log.Fatalf("error parsing net if config: %v", err)
	}
	// mounts may inherit period and entities
	nn := []*netIf{}
	for _, name := range cfg.Interfaces {
		mCfg := netIfConfig{
			pollerConfig: cfg.pollerConfig,
			Entities:     cfg.Entities,
		}
		yCfg := ifCfg[name]
		err := yCfg.Decode(&mCfg)
		if err != nil {
			log.Fatalf("error reading net %s config: %v", name, err)
		}

		nn = append(nn, newNetIf(name, &mCfg))
	}
	return &nets{nn: nn}
}

func (n *nets) Config() []EntityConfig {
	var config []EntityConfig
	for _, netif := range n.nn {
		config = append(config, netif.Config()...)
	}
	return config
}

func (n *nets) Publish() {
	for _, netif := range n.nn {
		netif.publish()
	}
}

func (n *nets) Sync(ps PubSub) {
	for _, netif := range n.nn {
		netif.Sync(ps)
	}
}

func (n *nets) Close() {
}

type gauge struct {
	valid bool
	value uint64
}

func (g gauge) delta(new gauge) uint64 {
	if g.valid && new.valid && (g.value < new.value) {
		return new.value - g.value
	}
	return 0
}

func (g gauge) rate(new gauge, td time.Duration) float64 {
	return float64(g.delta(new)) / td.Seconds()
}

type link struct {
	operstate string
	carrier   string
}

type netIf struct {
	name          string
	statsEntities map[string]bool
	linkEntities  map[string]bool
	link          link
	online        bool
	linkPoller    *PolledSensor
	statsPoller   *PolledSensor
	ps            PubSub
	gauges        map[string]gauge
	lastTime      time.Time
	linkMsg       string
	statsMsg      string
}

func (n *netIf) publish() {
	if n.linkPoller != nil {
		n.publishLink()
	}
	if n.statsPoller != nil {
		n.publishStats()
	}
}

func (n *netIf) publishLink() {
	n.ps.Publish("/"+n.name, n.linkMsg)
}

func (n *netIf) publishStats() {
	n.ps.Publish("/"+n.name+"/stats", n.statsMsg)
}

func (n *netIf) RefreshLink(forced bool) {
	changed := forced
	if n.linkEntities["operstate"] {
		opst := n.readStatus("operstate")
		if n.link.operstate != opst {
			changed = true
			n.link.operstate = opst
		}
	}
	if n.linkEntities["carrier"] {
		c := n.readStatus("carrier")
		if n.link.carrier != c {
			changed = true
			n.link.carrier = c
		}
	}
	if changed {
		fields := []string{}
		if n.linkEntities["operstate"] {
			fields = append(fields, fmt.Sprintf(`"operstate": "%s"`, n.link.operstate))
		}
		if n.linkEntities["carrier"] {
			fields = append(fields, fmt.Sprintf(`"carrier": "%s"`, n.link.carrier))
		}
		n.linkMsg = fmt.Sprintf("{%s}", strings.Join(fields, ", "))
		n.publishLink()
	}
}

func (n *netIf) RefreshStats(_ bool) {
	oldg := map[string]gauge{}
	t := time.Now()
	var elapsed time.Duration
	if !n.lastTime.IsZero() {
		elapsed = t.Sub(n.lastTime)
	}
	n.lastTime = t
	for gname := range n.gauges {
		oldg[gname] = n.gauges[gname]
		n.gauges[gname] = n.readGauge(gname)
	}
	fields := []string{}
	for _, gname := range statsGauges {
		if n.statsEntities[gname] {
			fields = append(fields, fmt.Sprintf(`"%s": %d`, gname, n.gauges[gname].value))
		}
	}
	for _, r := range statsRates {
		if n.statsEntities[r.rate] {
			rate := float64(0)
			if elapsed > 0 {
				rate = oldg[r.gauge].rate(n.gauges[r.gauge], elapsed) * r.scaling
			}
			fields = append(fields, fmt.Sprintf(`"%s": %0.2f`, r.rate, rate))
		}
	}
	n.statsMsg = fmt.Sprintf("{%s}", strings.Join(fields, ", "))
	n.publishStats()
}

func (n *netIf) readStatus(fname string) string {
	v, err := os.ReadFile("/sys/class/net/" + n.name + "/" + fname)
	if err == nil {
		return strings.TrimSpace(string(v))
	}
	return "unknown"
}

func (n *netIf) readGauge(gname string) gauge {
	g := gauge{}
	fname := "/sys/class/net/" + n.name + "/statistics/" + gname
	v, err := os.ReadFile(fname)
	if err == nil {
		v, err := strconv.ParseUint(strings.TrimSpace(string(v)), 10, 64)
		if err == nil {
			g.valid = true
			g.value = v
		}
	}
	return g
}

func (n *netIf) Close() {
	n.linkPoller.Close()
	n.statsPoller.Close()
}

func (n *netIf) Sync(ps PubSub) {
	n.ps = ps
	n.linkPoller.Sync(ps)
	n.statsPoller.Sync(ps)
}

var statsGauges = []string{
	"rx_bytes",
	"tx_bytes",
	"rx_packets",
	"tx_packets",
}

// Rate pairs the rate to the underlying gauge
type Rate struct {
	rate    string
	gauge   string
	scaling float64
}

var statsRates = []Rate{
	{"rx_throughput", "rx_bytes", 8},
	{"tx_throughput", "tx_bytes", 8},
	{"rx_packet_rate", "rx_packets", 1},
	{"tx_packet_rate", "tx_packets", 1},
}

var statsEntities = []string{
	"rx_bytes",
	"tx_bytes",
	"rx_throughput",
	"tx_throughput",
	"rx_packets",
	"tx_packets",
	"rx_packet_rate",
	"tx_packet_rate",
}

var linkEntities = []string{
	"operstate",
	"carrier",
}

func newNetIf(name string, cfg *netIfConfig) *netIf {
	// link and stats may inherit period
	if len(cfg.Link.Period) == 0 {
		cfg.Link.Period = cfg.Period
	}
	if len(cfg.Stats.Period) == 0 {
		cfg.Stats.Period = cfg.Period
	}
	se := map[string]bool{}
	le := map[string]bool{}
	for _, e := range cfg.Entities {
		if slices.Contains(statsEntities, e) {
			se[e] = true
		} else if slices.Contains(linkEntities, e) {
			le[e] = true
		}
	}
	n := netIf{
		name:          name,
		statsEntities: se,
		linkEntities:  le,
		online:        getLink(),
		ps:            StubPubSub{},
		gauges:        map[string]gauge{},
	}
	if se["rx_bytes"] || se["rx_throughput"] {
		n.gauges["rx_bytes"] = n.readGauge("rx_bytes")
	}
	if se["tx_bytes"] || se["tx_throughput"] {
		n.gauges["tx_bytes"] = n.readGauge("tx_bytes")
	}
	if se["rx_packets"] || se["rx_packet_rate"] {
		n.gauges["rx_packets"] = n.readGauge("rx_packets")
	}
	if se["tx_packets"] || se["tx_packet_rate"] {
		n.gauges["tx_packets"] = n.readGauge("tx_packets")
	}
	if len(le) > 0 {
		n.linkPoller = &PolledSensor{
			topic:  "/" + name,
			poller: NewPoller(&cfg.Link, n.RefreshLink),
			ps:     StubPubSub{},
		}
	}
	if len(se) > 0 {
		n.statsPoller = &PolledSensor{
			topic:  "/" + name + "/stats",
			poller: NewPoller(&cfg.Stats, n.RefreshStats),
			ps:     StubPubSub{},
		}
	}
	return &n
}

func (n *netIf) Config() []EntityConfig {
	var config []EntityConfig
	if n.linkPoller != nil {
		if n.linkEntities["operstate"] {
			cfg := map[string]any{
				"name":           "net " + n.name,
				"state_topic":    "~/net/" + n.name,
				"value_template": "{{value_json.operstate | is_defined}}",
				"device_class":   "connectivity",
				"payload_on":     "up",
				"payload_off":    "down",
			}
			if strings.HasPrefix(n.name, "wlan") {
				cfg["icon"] = "mdi:wifi-check"
			}
			config = append(config, EntityConfig{n.name + "-operstate", "binary_sensor", cfg})
		}
		if n.linkEntities["carrier"] {
			cfg := map[string]any{
				"name":           "net " + n.name + " carrier",
				"state_topic":    "~/net/" + n.name,
				"value_template": "{{value_json.carrier | is_defined}}",
				"device_class":   "connectivity",
				"payload_on":     "1",
				"payload_off":    "0",
			}
			if strings.HasPrefix(n.name, "wlan") {
				cfg["icon"] = "mdi:wifi"
			}
			config = append(config, EntityConfig{n.name + "-carrier", "binary_sensor", cfg})
		}
	}
	for e := range n.statsEntities {
		cfg := map[string]any{
			"name": fmt.Sprintf("net %s %s", n.name,
				strings.ReplaceAll(e, "_", " ")),
			"state_topic":    fmt.Sprintf("~/net/%s/stats", n.name),
			"value_template": fmt.Sprintf("{{value_json.%s | is_defined}}", e),
		}
		if strings.HasSuffix(e, "_bytes") {
			cfg["unit_of_measurement"] = "bytes"
		} else if strings.HasSuffix(e, "_throughput") {
			cfg["unit_of_measurement"] = "bps"
		} else if strings.HasSuffix(e, "_packets") {
			cfg["unit_of_measurement"] = "pkts"
		} else if strings.HasSuffix(e, "_packet_rate") {
			cfg["unit_of_measurement"] = "pps"
		}

		if strings.HasPrefix(n.name, "wlan") {
			if strings.HasPrefix(e, "rx_") {
				cfg["icon"] = "mdi:wifi-arrow-down"
			} else if strings.HasPrefix(e, "tx_") {
				cfg["icon"] = "mdi:wifi-arrow-up"
			}
		} else {
			if strings.HasPrefix(e, "rx_") {
				cfg["icon"] = "mdi:upload-network-outline"
			} else if strings.HasPrefix(e, "tx_") {
				cfg["icon"] = "mdi:download-network-outline"
			}
		}

		config = append(config, EntityConfig{n.name + "-" + e, "sensor", cfg})
	}
	return config
}
