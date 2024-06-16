<!--
SPDX-FileCopyrightText: 2022 Kent Gibson <warthog618@gmail.com>

SPDX-License-Identifier: MIT
-->

# dunnart

[![Build Status](https://img.shields.io/github/actions/workflow/status/warthog618/dunnart/go.yml?logo=github&branch=master)](https://github.com/warthog618/dunnart/actions/workflows/go.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/warthog618/dunnart)](https://goreportcard.com/report/github.com/warthog618/dunnart)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/warthog618/dunnart/blob/master/LICENSE)

Lightweight remote system monitoring over MQTT for Home Assistant.

Lightweight in that the binary is standalone and has no dependencies - just copy the binary to your target with a suitable config file and go.

Lightweight in that it has a small footprint, in terms of both the binary size and CPU load.

Lightweight in that it only monitors what you want it to - it doesn't bloat your MQTT and Home Assistant with an abundance of sensors that you don't care about.

Lightweight in that the sensors that you want to monitor are automatically detected by the Home Assistant MQTT integration.  Just specify what you want to monitor in the **dunnart** config file and they appear in Home Assistant.

Currently tested on Raspberry Pis and OpenWrt routers, but should work on any Linux based device.

## Example Setup

A device with this configuration:

```yaml
modules: [cpu, mem, fs]

fs:
  mountpoints: [root, home]
  root.path: "/"
  home.path: "/home"

mqtt:
  broker: tcp://<mqtt server>:1883
  username: <username>
  password: <password>
```

produces these sensors in Home Assistant:

![Device Sensors](https://github.com/warthog618/dunnart/blob/master/readme/device-sensors.png?raw=true)

The cpu usage gives some indication of the system load with **dunnart** running.

This particular device doesn't actually have a /home mountpoint (i.e. the config is intentionally broken) so it shows as disconnected, and the usage as unavailable.

If the daemon is stopped, or the device stops for whatever reason, then all the sensors become unavailable:

![Device Sensors - unavailable](https://github.com/warthog618/dunnart/blob/master/readme/device-sensors-unavailable.png?raw=true)

with the exception of the device status itself which shows as disconnected/offline.

## Installation

The basic steps:

- get a copy of the repo
- build for your target using `make <target>` for the appropriate target type, or alternatively call `go build` with the compile options suitable for your target.
- customise dunnart.yaml
- copy the **dunnart** binary and config to your target
- run the daemon to check everything works
- configure the init system to start the daemon - an example dunnart.service for systemd is provided.

## Configuration

The top level of the configuration contains the modules being loaded, the global defaults for common configuration, the mqtt and home assistant configuration sections, and optionally the configuration for each of the modules.

Sections only need to be present to override default values, or where no default value is provided.

|Field|Description|Default|
|-----|------|:-----:|
|modules|The modules to be loaded|-|
|period|Globally override the period for polled sensors|None - set by modules|

### MQTT

The mqtt section specifies the connection to the MQTT broker.

This section is required as there is no default provided for the broker URL.

|Field|Description|Default|
|-----|------|:-----:|
|broker|broker URL|-|
|username|broker authentication username|None set|
|password|broker authentication password|None set|
|base_topic|The topic prefix for all generated entities|dunnart/*hostname*|

### Home Assistant

The Home Assistant section specifies how to detect HA MQTT reconnection and how to publish entity config messages so HA can automatically discover the entities and assign them to a device corresponding to the **dunnart** host.

Discovery relies on HA having discovery enabled and publishing to the birth_message_topic when it first connects to the MQTT broker, so both `Enable discovery` and `Enable birth message` must be set in the MQTT integration options on HA, and the `Discovery prefix` and `Birth message topic` must match in the HA and **dunnart** configurations.

The **dunnart** defaults correspond to the default HA configuration, in which case the homeassistant section can be omitted.  For non-default setups the **bold** settings must be set to match the HA configuration.

|Field|Description|Default|
|-----|------|:-----:|
|birth_message_topic|The topic HA birth message topic|**homeassistant/status**|
|discovery.prefix|The prefix for the topics to publish sensor config messages|**homeassistant**|
|discovery.node_id|The name of the device that the MQTT integration will add to HA and that the entities will be assigned to.|*hostname*|
|discovery.mac_source|A list of interfaces to use to provide a unique MAC address to identify this host device.  The first active listed interface is used.  This setting is ignored if *mac* is set. |[eth0, enp3s0, wlan0]|
|discovery.mac|A unique MAC address to identify this host device.  If set this overrides *mac_source*. |Not set|
|discovery.status_delay|A period between publishing entity config and status to allow HA time to register new entities before receiving the entity status|15s|

### Modules

As per the Home Assistant section, the module sections are only required to override the default settings.

#### CPU

|Field|Description|Default|
|-----|------|:-----:|
|entities|The cpu sensors to expose|[used_percent, temperature]|
|period|The polling period for all cpu sensors|1m|
|temperature.path|The path to the file containing the CPU temperature|/sys/class/thermal/thermal_zone0/temp|

Supported entities:

- used_percent
- temperature
- uptime

#### Memory (mem)

|Field|Description|Default|
|-----|------|:-----:|
|entities|The mem sensors to expose|[ram_used_percent, swap_used_percent]|
|period|The polling period for all memory sensors|1m|

Supported entities:

- ram_used_percent
- swap_used_percent

#### Filesystem (fs)

|Field|Description|Default|
|-----|------|:-----:|
|period|The polling period for the mount point sensors|10m|
|mountpoints|The list of mount points to monitor|-|
|*mountpoint*.path|The path of the mount point|-|
|*mountpoint*.period|The polling period for the sensors on this interface|fs.period|

For a particular host, the mount points available are listed by `mount`.

Supported entities:

- mounted
- used_percent

#### Network Interface (net)

|Field|Description|Default|
|-----|------|:-----:|
|entities|The network interface sensors to expose for each interface|[operstate, rx_bytes, rx_throughput, tx_bytes, tx_throughput]|
|period|The polling period for the network interface sensors|1m|
|interfaces|The list of interfaces to monitor|-|
|*interface*.entities|The network interface sensors to expose for this interface|net.entities|
|*interface*.period|The polling period for the sensors on this interface|net.period|

For a particular host, the interfaces available are listed in `/sys/class/net`.

Supported entities:

- operstate (interface up or down)
- carrier (carrier medium connected)
- rx_bytes
- rx_packets
- rx_packet_rate
- rx_throughput
- tx_bytes
- tx_packets
- tx_packet_rate
- tx_throughput

#### System Info

|Field|Description|Default|
|-----|------|:-----:|
|entities|The system info sensors to expose|[kernel_release, os_release]|
|period|The polling period for the system info sensors|6h|

The info is drawn from `apt`, `uname` and `/etc/os-release`.

Supported entities:

- apt_status (APT upgrades are available)
- apt_upgradable (number of upgradable APT packages)
- machine (uname -m)
- kernel_name (uname -s)
- kernel_release (uname -r)
- kerrnel_version (uname -v)
- machine (uname -m)
- os_name (/etc/os-release NAME)
- os_release (/etc/os-release PRETTY_NAME)
- os_version (/etc/os-release/VERSION)

#### WAN

|Field|Description|Default|
|-----|------|:-----:|
|entities|The wan sensors to expose|[link, ip]|
|link.period|The polling period for the WAN link sensor|1m|
|ip.period|The polling period for the IP address sensor|15m|

Supported entities:

- link
- ip

## Background

This is a spin-off from a couple of daemons I wrote some time ago to control some devices over MQTT.  Over time I modified those to integrate into Home Assistant and used [glances](https://nicolargo.github.io/glances/) to monitor the Raspberry Pis the daemons were running on.

I came to realise that *glances* was overkill for what I needed, and so extended those daemons to provide the system monitoring that I required instead.

I then realised that it would be useful to be able to monitor the other devices in my setup, so moved the common system monitoring into **dunnart** and the old daemons became special case extensions of that.

## Features

### Home Assistant Integration

Entities are discoverable by Home Assistant via the MQTT integration.  Discovery must be enabled in the MQTT integration, and **dunnart** must be configured with the corresponding discovery topic prefix.

All entities are assigned to a device, corresponding to the **dunnart** host, on Home Assistant.

Sensor availability is automatically dependent on the availability of the **dunnart** daemon.

**dunnart** subscribes to the Home Assistant birth message topic and will re-advertise the sensor config and republish the sensor states whenever Home Assistant reconnects to MQTT.

### Modular Design

Entities are grouped into modules, so groups of entities can be easily enabled or disabled.  Modules must be explicitly enabled, and disabled modules use no resources.

The existing set of provided modules includes cpu, memory, file system, net and wan.  The modules and entities provided are currently minimal, being those I found sufficient to monitor the health of my setup.

The current entities include:

- device availability
- cpu used percentage
- cpu temperature
- ram used percentage
- swap used percentage
- file system mountpoint used percentage
- network interface connectivity and statistics
- os and kernel version
- uptime
- wan link availability
- wan IP address

Refer to the module configuration sections for a complete list of supported entities.

### Polling Rate

The polling rate for polled sensors is individually controllable, both via configuration and via MQTT.  e.g. cpu load may be checked every minute while file system usage may checked every 10 minutes.  To update the polling period, publish a message with the new polling period to `<sensor topic>/rqd/poll_period`.

Sensors may also be requested to update on demand via MQTT - publish a message to `<sensor topic>/rqd` and the sensor will refresh and publish its current state.

## Roadmap

Additional modules under consideration:

- GPIO (input pins as sensors, output pins as switches)
- RPi Power
- Exec (execute an arbitrary command on the remote host)
