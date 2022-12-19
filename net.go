// Copyright © 2022 Kent Gibson <warthog618@gmail.com>.
//
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
	"time"

	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("net", newNets)
}

type Nets struct {
	nn []*NetIf
}

func newNets(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("period", "1m")
	defCfg.Set("entities", []string{
		"operstate",
		"rx_bytes",
		"tx_bytes",
		"rx_throughput",
		"tx_throughput",
	},
	)
	cfg.Append(defCfg)
	// mounts may inherit period and entities
	mDefCfg := dict.New()
	mDefCfg.Set("period", cfg.MustGet("period").String())
	mDefCfg.Set("entities", cfg.MustGet("entities").StringSlice())
	nn := []*NetIf{}
	for _, name := range cfg.MustGet("interfaces").StringSlice() {
		mCfg := cfg.GetConfig(name)
		mCfg.Append(mDefCfg)
		nn = append(nn, newNetIf(name, mCfg))
	}
	return &Nets{nn: nn}
}

func (n *Nets) Config() []EntityConfig {
	var config []EntityConfig
	for _, netif := range n.nn {
		config = append(config, netif.Config()...)
	}
	return config
}

func (n *Nets) Sync(ps PubSub) {
	for _, netif := range n.nn {
		netif.Sync(ps)
	}
}

func (n *Nets) Close() {
}

type Gauge struct {
	valid bool
	value uint64
}

func (g Gauge) delta(new Gauge) uint64 {
	if g.valid && new.valid && (g.value < new.value) {
		return new.value - g.value
	}
	return 0
}

func (g Gauge) rate(new Gauge, td time.Duration) float64 {
	return float64(g.delta(new)) / td.Seconds()
}

type Link struct {
	operstate string
	carrier   string
}

type NetIf struct {
	name          string
	statsEntities map[string]bool
	linkEntities  map[string]bool
	link          Link
	online        bool
	linkPoller    *PolledSensor
	statsPoller   *PolledSensor
	ps            PubSub
	gauges        map[string]Gauge
	lastTime      time.Time
}

func (n *NetIf) RefreshLink(forced bool) {
	changed := forced
	if n.linkEntities["operstate"] {
		opst := n.read_status("operstate")
		if n.link.operstate != opst {
			changed = true
			n.link.operstate = opst
		}
	}
	if n.linkEntities["carrier"] {
		c := n.read_status("carrier")
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
		value := fmt.Sprintf("{%s}", strings.Join(fields, ", "))
		n.ps.Publish("/"+n.name, value)
	}
}

func (n *NetIf) RefreshStats(forced bool) {
	oldg := map[string]Gauge{}
	t := time.Now()
	var elapsed time.Duration
	if !n.lastTime.IsZero() {
		elapsed = t.Sub(n.lastTime)
	}
	n.lastTime = t
	for gname := range n.gauges {
		oldg[gname] = n.gauges[gname]
		n.gauges[gname] = n.read_gauge(gname)
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
	value := fmt.Sprintf("{%s}", strings.Join(fields, ", "))
	n.ps.Publish("/"+n.name+"/stats", value)
}

func (n *NetIf) read_status(fname string) string {
	v, err := ioutil.ReadFile("/sys/class/net/" + n.name + "/" + fname)
	if err == nil {
		return strings.TrimSpace(string(v))
	}
	return "unknown"
}

func (n *NetIf) read_gauge(gname string) Gauge {
	g := Gauge{}
	fname := "/sys/class/net/" + n.name + "/statistics/" + gname
	v, err := ioutil.ReadFile(fname)
	if err == nil {
		v, err := strconv.ParseUint(strings.TrimSpace(string(v)), 10, 64)
		if err == nil {
			g.valid = true
			g.value = v
		}
	}
	return g
}

func (w *NetIf) Close() {
	w.linkPoller.Close()
	w.statsPoller.Close()
}

func (w *NetIf) Sync(ps PubSub) {
	w.ps = ps
	w.linkPoller.Sync(ps)
	w.statsPoller.Sync(ps)
}

var statsGauges = []string{
	"rx_bytes",
	"tx_bytes",
	"rx_packets",
	"tx_packets",
}

// pairing of rate to underlying gauge
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

func ss_contains(ss []string, name string) bool {
	for _, s := range ss {
		if s == name {
			return true
		}
	}
	return false
}

func newNetIf(name string, cfg *config.Config) *NetIf {
	defCfg := dict.New()
	// link and stats may inherit period
	defCfg.Set("link.period", cfg.MustGet("period").String())
	defCfg.Set("stats.period", cfg.MustGet("period").String())
	cfg.Append(defCfg)
	se := map[string]bool{}
	le := map[string]bool{}
	for _, e := range cfg.MustGet("entities").StringSlice() {
		if ss_contains(statsEntities, e) {
			se[e] = true
		} else if ss_contains(linkEntities, e) {
			le[e] = true
		}
	}
	n := NetIf{
		name:          name,
		statsEntities: se,
		linkEntities:  le,
		online:        getLink(),
		ps:            StubPubSub{},
		gauges:        map[string]Gauge{},
	}
	if se["rx_bytes"] || se["rx_throughput"] {
		n.gauges["rx_bytes"] = n.read_gauge("rx_bytes")
	}
	if se["tx_bytes"] || se["tx_throughput"] {
		n.gauges["tx_bytes"] = n.read_gauge("tx_bytes")
	}
	if se["rx_packets"] || se["rx_packet_rate"] {
		n.gauges["rx_packets"] = n.read_gauge("rx_packets")
	}
	if se["tx_packets"] || se["tx_packet_rate"] {
		n.gauges["tx_packets"] = n.read_gauge("tx_packets")
	}
	if len(le) > 0 {
		n.linkPoller = &PolledSensor{
			topic:  "/" + name,
			poller: NewPoller(cfg.MustGet("link.period").Duration(), n.RefreshLink),
			ps:     StubPubSub{},
		}
	}
	if len(se) > 0 {
		n.statsPoller = &PolledSensor{
			topic:  "/" + name + "/stats",
			poller: NewPoller(cfg.MustGet("stats.period").Duration(), n.RefreshStats),
			ps:     StubPubSub{},
		}
	}
	return &n
}

func (n *NetIf) Config() []EntityConfig {
	var config []EntityConfig
	if n.linkPoller != nil {
		if n.linkEntities["operstate"] {
			cfg := map[string]interface{}{
				"name":           "{{.NodeId}} net " + n.name,
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
			cfg := map[string]interface{}{
				"name":           "{{.NodeId}} net " + n.name + " carrier",
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
		cfg := map[string]interface{}{
			"name": fmt.Sprintf("{{.NodeId}} net %s %s", n.name,
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
