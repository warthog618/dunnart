// SPDX-FileCopyrightText: 2019 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net"
	"time"

	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("wan", newWAN)
}

func onlineString(online bool) string {
	if online {
		return "online"
	}
	return "offline"
}

type wan struct {
	online     bool
	ip         string
	linkPoller *PolledSensor
	ipPoller   *PolledSensor
	ps         PubSub
}

func (w *wan) Publish() {
	if w.linkPoller != nil {
		w.ps.Publish("", onlineString(w.online))
	}
	if w.ipPoller != nil {
		w.ps.Publish("/ip", w.ip)
	}
}

func (w *wan) RefreshLink(forced bool) {
	online := getLink()
	if w.online != online || forced {
		w.online = online
		w.ps.Publish("", onlineString(w.online))
		if w.ipPoller != nil {
			w.ipPoller.poller.Refresh(false)
		}
	}
}

func (w *wan) RefreshIP(forced bool) {
	ip := getIP()
	if w.ip != ip || forced {
		w.ip = ip
		w.ps.Publish("/ip", w.ip)
	}
}

func (w *wan) Close() {
	w.linkPoller.Close()
	w.ipPoller.Close()
}

func (w *wan) Sync(ps PubSub) {
	w.ps = ps
	w.linkPoller.Sync(ps)
	w.ipPoller.Sync(ps)
}

func newWAN(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("link.period", "1m")
	defCfg.Set("ip.period", "15m")
	defCfg.Set("entities", []string{"link", "ip"})
	cfg.Append(defCfg)
	entities := map[string]bool{}
	for _, e := range cfg.MustGet("entities").StringSlice() {
		entities[e] = true
	}
	w := wan{
		online: getLink(),
		ps:     StubPubSub{},
	}
	if entities["link"] {
		w.linkPoller = &PolledSensor{
			topic:  "",
			poller: NewPoller(cfg.MustGet("link.period").Duration(), w.RefreshLink),
			ps:     StubPubSub{},
		}
	}
	if entities["ip"] {
		w.ipPoller = &PolledSensor{
			topic:  "/ip",
			poller: NewPoller(cfg.MustGet("ip.period").Duration(), w.RefreshIP),
			ps:     StubPubSub{},
		}
	}
	return &w
}

func (w *wan) Config() []EntityConfig {
	var config []EntityConfig
	if w.linkPoller != nil {
		cfg := map[string]interface{}{
			"name":         "WAN",
			"state_topic":  "~/wan",
			"device_class": "connectivity",
			"icon":         "mdi:wan",
			"payload_on":   "online",
			"payload_off":  "offline",
		}
		config = append(config, EntityConfig{"link", "binary_sensor", cfg})
	}
	if w.ipPoller != nil {
		cfg := map[string]interface{}{
			"name":        "WAN IP",
			"state_topic": "~/wan/ip",
			"icon":        "mdi:ip",
		}
		config = append(config, EntityConfig{"ip", "sensor", cfg})
	}
	return config
}

type dialer func(ctx context.Context, network, address string) (net.Conn, error)

func lookupGoogle(d dialer) (addrs []string, err error) {
	r := net.Resolver{
		PreferGo: true,
		Dial:     d,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.LookupHost(ctx, "www.google.com")
}

func getLink() bool {
	dialers := []dialer{CloudFlareDNSDialer, GoogleDNSDialer, OpenDNSDialer}
	for _, dialer := range dialers {
		_, err := lookupGoogle(dialer)
		if err == nil {
			return true
		}
	}
	return false
}

func getIP() string {
	r := net.Resolver{
		PreferGo: true,
		Dial:     OpenDNSDialer,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	addr, err := r.LookupHost(ctx, "myip.opendns.com")
	if err != nil {
		return "unknown"
	}
	return addr[0]
}

// CloudFlareDNSDialer connects to a CloudFlare DNS server
func CloudFlareDNSDialer(ctx context.Context, _, _ string) (net.Conn, error) {
	d := net.Dialer{}
	return d.DialContext(ctx, "udp", "1.1.1.1:53")
}

// GoogleDNSDialer connects to a Google DNS server
func GoogleDNSDialer(ctx context.Context, _, _ string) (net.Conn, error) {
	d := net.Dialer{}
	return d.DialContext(ctx, "udp", "8.8.8.8:53")
}

// OpenDNSDialer connects to an OpenDNS DNS server
// Note that this assumes the default DNS lookup is functional.
func OpenDNSDialer(ctx context.Context, _, _ string) (net.Conn, error) {
	addrs, err := net.LookupHost("resolver1.opendns.com")
	if err != nil {
		return nil, err
	}
	d := net.Dialer{}
	return d.DialContext(ctx, "udp", addrs[0]+":53")
}
