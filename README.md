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

## Background

This is a spin-off from a couple of daemons I wrote some time ago to control some devices over MQTT.  Over time I modified those to integrate into Home Assistant and used [glances](https://nicolargo.github.io/glances/) to monitor the Raspberry Pis the daemons were running on.

I came to realise that *glances* was overkill for what I needed, and so extended those daemons to provide the system monitoring that I required instead.

I then realised that it would be useful to be able to monitor the other devices in my setup, so moved the common system monitoring into **dunnart** and the old daemons became special case extensions of that.

## Features

### Home Assistant Integration

Sensors are discoverable by Home Assistant via the MQTT integration.  Discovery must be enabled in the MQTT integration, and **dunnart** must be configured with the corresponding discovery topic prefix.

All sensors are assigned to a device, corresponding to the **dunnart** host, on Home Assistant.

Sensor availability is automatically dependent on the availability of the **dunnart** daemon.

If the `trigger_topic` is set in the configuration then **dunnart** will re-advertise the sensor config whenever Home Assistant reconnects to MQTT, else the sensor config is sent once on startup with the retain flag set.

### Modular Design

Sensors are grouped into modules, so groups of sensors can be easily enabled or disabled.  Modules must be explicitly enabled, and disabled modules use no resources.

The existing set of provided modules includes cpu, memory, file system and wan.  The modules and sensors  provided are currently minimal, being those I found sufficient to monitor the health of my setup.

The current sensors include:

- device availability
- cpu used percent
- cpu temperature
- ram used percent
- swap used percent
- file system mountpoint used percent
- network interface connectivity and statistics
- wan link availability
- wan IP address

Raise an issue if there is something additional you need to monitor.

### Polling Rate

The polling rate for polled sensors is individually controllable, both via configuration and via MQTT.  e.g. cpu load may be checked every minute while file system usage may checked every 10 minutes.  To update the polling period, publish a message with the new polling period to `<sensor topic>/rqd/poll_period`.

Sensors may also be requested to update on demand via MQTT - publish a message to `<sensor topic>/rqd` and the sensor will refresh and publish its current state.
