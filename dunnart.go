// SPDX-FileCopyrightText: 2019 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

// Dunnart is a lightweight system monitor over MQTT for HomeAssistant
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gopkg.in/yaml.v3"
)

const (
	mustQos byte = 1
)

var (
	version = "undefined"
)

type discoveryConfig struct {
	Prefix      string
	NodeID      string   `yaml:"node_id"`
	MacSource   []string `yaml:"mac_source"`
	Mac         string
	StatusDelay string `yaml:"status_delay"`
	UniqueID    string `yaml:"unique_id"`
}

type homeAssistantConfig struct {
	BirthMessageTopic string `yaml:"birth_message_topic"`
	Discovery         discoveryConfig
}

type mqttConfig struct {
	Broker    string
	Username  string
	Password  string
	BaseTopic string `yaml:"base_topic"`
}

type config struct {
	HomeAssistant homeAssistantConfig
	Mqtt          mqttConfig
	Modules       []string
	mm            map[string]yaml.Node
}

func loadConfig() config {
	cfg := config{
		HomeAssistant: homeAssistantConfig{
			BirthMessageTopic: "homeassistant/status",
			Discovery: discoveryConfig{
				StatusDelay: "15s",
				Prefix:      "homeassistant",
				MacSource:   []string{"eth0", "enu1u1", "enp3s0", "wlan0"},
			},
		},
	}

	host, err := os.Hostname()
	if err == nil {
		cfg.Mqtt.BaseTopic = "dunnart/" + host
		cfg.HomeAssistant.Discovery.NodeID = host
	}
	configFile, ok := os.LookupEnv("DUNNART_CONFIG_FILE")
	if !ok {
		flag.StringVar(&configFile, "c", "dunnart.yaml", "configuration file")
		flag.Parse()
	}
	ycfg, err := os.ReadFile(configFile)
	if err != nil {
		log.Fatalf("error reading config file: %v", err)
	}
	// structured read for main config
	err = yaml.Unmarshal(ycfg, &cfg)
	if err != nil {
		log.Fatalf("error parsing config file: %v", err)
	}
	// unstructured read for module config
	var mm map[string]yaml.Node
	err = yaml.Unmarshal(ycfg, &mm)
	if err != nil {
		log.Fatalf("error parsing config: %v", err)
	}
	cfg.mm = make(map[string]yaml.Node)
	for _, m := range cfg.Modules {
		cfg.mm[m] = mm[m]
	}
	return cfg
}

func newMQTTOpts(cfg *mqttConfig) *mqtt.ClientOptions {
	// OrderMatters defaults to true - required for QoS1 ordering
	opts := mqtt.NewClientOptions().AddBroker(cfg.Broker)
	if len(cfg.Username) > 0 {
		opts = opts.SetUsername(cfg.Username)
	}
	if len(cfg.Password) > 0 {
		opts = opts.SetPassword(cfg.Password)
	}
	return opts
}

type dunnart struct {
	ps PubSub
}

func (d *dunnart) Publish() {
	d.ps.Publish("", "online")
	d.ps.Publish("/version", version)
}

func (d *dunnart) Sync(ps PubSub) {
	d.ps = ps
	d.Publish()
}

func (d *dunnart) Config() []EntityConfig {
	var config []EntityConfig
	cfg := map[string]any{
		"name":         "status",
		"object_id":    "{{.NodeID}}_status",
		"state_topic":  "~",
		"device_class": "connectivity",
		"payload_on":   "online",
		"payload_off":  "offline",
	}
	config = append(config, EntityConfig{"status", "binary_sensor", cfg})
	return config
}

func connect(mc mqtt.Client, done <-chan struct{}) error {
	tok := mc.Connect()
	select {
	case <-tok.Done():
		return tok.Error()
	case <-done:
		return nil
	}
}

func initialConnect(mc mqtt.Client, done <-chan struct{}) {
	err := connect(mc, done)
	if err == nil {
		return
	}
	log.Printf("connect error: %s", err)
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for range t.C {
		err = connect(mc, done)
		if err != nil {
			log.Printf("connect error: %s", err)
		} else {
			return
		}
	}
}

// ModuleFactory creates a module with the given config.
type ModuleFactory func(cfg *yaml.Node) SyncCloser

var moduleFactories = map[string]ModuleFactory{}

// RegisterModule provides the mapping from module name, as found in the
// config file, to the ModuleFactory used to construct the module.
func RegisterModule(name string, mf ModuleFactory) {
	moduleFactories[name] = mf
}

func main() {
	log.SetFlags(0)

	cfg := loadConfig()

	// capture exit signals to ensure defers are called on the way out.
	sigdone := make(chan os.Signal, 1)
	signal.Notify(sigdone, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigdone)
	done := make(chan struct{})
	go func() {
		select {
		case <-sigdone:
			close(done)
		case <-done:
		}
	}()

	ss := map[string]Syncer{
		"": &dunnart{},
	}

	for modName, modCfg := range cfg.mm {
		factory := moduleFactories[modName]
		if factory == nil {
			log.Fatalf("unsupported sensor: %s", modName)
		}
		mod := factory(&modCfg)
		ss[modName] = mod
		defer mod.Close()
	}

	connect := make(chan int)
	mOpts := newMQTTOpts(&cfg.Mqtt).
		SetWill(cfg.Mqtt.BaseTopic, "offline", mustQos, false).
		SetOnConnectHandler(func(mc mqtt.Client) {
			select {
			case connect <- 0:
			case <-done:
			}
		})

	mc := mqtt.NewClient(mOpts)
	initialConnect(mc, done)
	defer mc.Disconnect(0)

	disco := newDiscovery(&cfg.HomeAssistant.Discovery, ss, cfg.Mqtt.BaseTopic)
	// delay for when ha sees the ads for the first time and is slow subscribing
	sdelay, err := time.ParseDuration(cfg.HomeAssistant.Discovery.StatusDelay)
	if err != nil {
		log.Fatalf("error parsing status_delay '%s': %v", cfg.HomeAssistant.Discovery.StatusDelay, err)
	}
	go func() {
		for {
			select {
			case <-done:
				return
			case <-connect:
				log.Print("mqtt connect")
				disco.advertise(mc)
				for modName, s := range ss {
					t := cfg.Mqtt.BaseTopic
					if len(modName) > 0 {
						t += "/" + modName
					}
					ps := mqttPubSub{mc, t}
					s.Sync(ps)
				}
				mc.Subscribe(cfg.HomeAssistant.BirthMessageTopic, mustQos,
					func(mc mqtt.Client, msg mqtt.Message) {
						if string(msg.Payload()) == "online" {
							disco.advertise(mc)
							time.Sleep(sdelay)
							for _, s := range ss {
								s.Publish()
							}
						}
					})
				time.Sleep(sdelay)
				for _, s := range ss {
					s.Publish()
				}

			}
		}
	}()
	<-done
}

type discovery struct {
	// map from topic to config for discoverable entities
	ents map[string]string
}

func newDiscovery(cfg *discoveryConfig, ss map[string]Syncer, baseTopic string) discovery {
	ents := map[string]string{}
	if len(cfg.Prefix) > 0 {
		mac, err := getMAC(cfg)
		if err != nil {
			log.Fatalf("discovery: %v", err)
		}
		uid := cfg.UniqueID
		if len(uid) == 0 {
			uid = "dnrt-" + strings.ReplaceAll(mac, ":", "")
		}
		baseCfg := map[string]any{
			"~": baseTopic,
			"device": map[string]any{
				"name":        cfg.NodeID,
				"connections": [][]string{{"mac", mac}},
			},
		}
		for modName, s := range ss {
			if a, ok := s.(discoverable); ok {
				for _, entity := range a.Config() {
					euid := uid
					if len(modName) > 0 {
						euid += "-" + modName
					}
					euid += "-" + entity.name
					topic := strings.Join(
						[]string{cfg.Prefix,
							entity.class,
							euid,
							"config"},
						"/")
					baseCfg["unique_id"] = euid
					baseCfg["object_id"] = strings.Join([]string{cfg.NodeID, modName, entity.name}, "_")
					config := normaliseConfig(entity.config, baseCfg)
					config = strings.ReplaceAll(config, "{{.NodeID}}", cfg.NodeID)
					ents[topic] = config
				}
			}
		}
	}
	return discovery{ents: ents}
}

func (d *discovery) advertise(mc mqtt.Client) {
	log.Print("advertise for ha discovery")
	for topic, config := range d.ents {
		mc.Publish(topic, mustQos, false, config)
	}
}

func getMAC(cfg *discoveryConfig) (string, error) {
	if len(cfg.Mac) > 0 {
		return cfg.Mac, nil
	}
	for _, source := range cfg.MacSource {
		v, err := os.ReadFile(fmt.Sprintf("/sys/class/net/%s/address", source))
		if err == nil {
			return strings.TrimSpace(string(v)), nil
		}
	}
	return "", errors.New("can't find a MAC address - check your homeassistant.discovery.mac_source configuration")
}

func normaliseConfig(cfg, baseCfg map[string]any) string {
	for k, v := range baseCfg {
		if _, exists := cfg[k]; !exists {
			cfg[k] = v
		}
	}

	if !configContains(cfg, "availability_topic") && !configContains(cfg, "availability") {
		cfg["availability_topic"] = "~"
	}
	if cfg["state_topic"] == "~" {
		delete(cfg, "availability_topic")
	}

	config, err := json.Marshal(cfg)
	if err != nil {
		log.Fatalf("failed to marshal JSON: %v", err)
	}

	return string(config)
}

func configContains(cfg map[string]any, key string) bool {
	_, ok := cfg[key]
	return ok
}

// Syncer is a type that syncs its state with MQTT.
type Syncer interface {
	// Check the current state of contained entities and publish any state changes.
	Sync(PubSub)
	// Publish the current state of the contained entities - no updates.
	Publish()
}

// SyncCloser is a Syncer that also provides a Close.
type SyncCloser interface {
	Syncer
	Close()
}

// EntityConfig defines how to map a module field into a HA entity.
type EntityConfig struct {
	// The name of the entity within the module
	name string
	// The class of the entity, e.g. sensor, binary_sensor, etc
	class string
	// The config message for the entity.
	// This is the base message that is normalised, adding in default fields
	// and performing template substitution, and converted to JSON and sent
	// to the broker.
	config map[string]any
}

type discoverable interface {
	Config() []EntityConfig
}

// PubSub is an interface which can both publish messages to topics,
// and subscribe to messages on topics.
type PubSub interface {
	Publish(string, any)
	Subscribe(string, func([]byte))
}

type mqttPubSub struct {
	mc        mqtt.Client
	baseTopic string
}

// Publish publishes a topic to the MQTT broker.
func (m mqttPubSub) Publish(topic string, value any) {
	log.Printf("publish %s '%s'", m.baseTopic+topic, fmt.Sprint(value))
	m.mc.Publish(m.baseTopic+topic, mustQos, false, fmt.Sprint(value))
}

// Subscribe subscribes to a topic on the MQTT broker.
func (m mqttPubSub) Subscribe(topic string, callback func([]byte)) {
	wrap := func(m mqtt.Client, msg mqtt.Message) {
		callback(msg.Payload())
	}
	log.Printf("subscribe %s", m.baseTopic+topic)
	m.mc.Subscribe(m.baseTopic+topic, mustQos, wrap)
}

// StubPubSub is an empty PubSub implementation.
type StubPubSub struct{}

// Publish does nothing.
func (s StubPubSub) Publish(_ string, _ any) {
}

// Subscribe does nothing.
func (s StubPubSub) Subscribe(_ string, _ func([]byte)) {
}
