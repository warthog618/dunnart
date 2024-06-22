// SPDX-FileCopyrightText: 2024 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/warthog618/config"
	"github.com/warthog618/config/dict"
)

func init() {
	RegisterModule("cmd", newCmds)
}

type cmd interface {
	discoverable
	SyncCloser
}

type cmds struct {
	cc []cmd
}

func newCmds(cfg *config.Config) SyncCloser {
	defCfg := dict.New()
	defCfg.Set("period", cfg.MustGet("period", config.WithDefaultValue("1h")).String())
	cc := []cmd{}
	ss := cfg.MustGet("binary_sensors").StringSlice()
	for _, name := range ss {
		mCfg := cfg.GetConfig(name)
		mCfg.Append(defCfg)
		cc = append(cc, newBinarySensorCmd(name, mCfg))
	}
	return &cmds{cc: cc}
}

func (c *cmds) Config() []EntityConfig {
	var config []EntityConfig
	for _, cmd := range c.cc {
		config = append(config, cmd.Config()...)
	}
	return config
}

func (c *cmds) Publish() {
	for _, cmd := range c.cc {
		cmd.Publish()
	}
}

func (c *cmds) Sync(ps PubSub) {
	for _, cmd := range c.cc {
		cmd.Sync(ps)
	}
}

func (c *cmds) Close() {
}

// Is a sensorCmd
type binarySensorCmd struct {
	PolledSensor
	name    string
	haName  string
	cmd     string
	timeout time.Duration
	err     error
	msg     string
	cfg     []EntityConfig
}

func newBinarySensorCmd(name string, cfg *config.Config) *binarySensorCmd {
	c := binarySensorCmd{name: name, cmd: cfg.MustGet("cmd").String()}
	timeout, err := cfg.Get("timeout")
	if err == nil {
		c.timeout = timeout.Duration()
	}
	c.topic = "/" + name
	haName, err := cfg.Get("name")
	if err == nil {
		c.haName = haName.String()
	} else {
		c.haName = "cmd " + name
	}
	c.poller = NewPoller(cfg.MustGet("period").Duration(), c.Refresh)
	ecfg := map[string]interface{}{
		"name":           c.haName,
		"state_topic":    "~/cmd" + c.topic,
		"value_template": "{{value_json.state}}",
		"payload_on":     "on",
		"payload_off":    "off",
	}
	dc, err := cfg.Get("device_class")
	if err == nil {
		ecfg["device_class"] = dc.String()
	}
	icon, err := cfg.Get("icon")
	if err == nil {
		ecfg["icon"] = icon.String()
	}
	c.cfg = append(c.cfg, EntityConfig{c.name, "binary_sensor", ecfg})
	return &c
}

func (c *binarySensorCmd) Config() []EntityConfig {
	return c.cfg
}

func (c *binarySensorCmd) update() bool {
	var cmd *exec.Cmd
	if c.timeout == 0 {
		cmd = exec.Command(c.cmd)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
		defer cancel()
		cmd = exec.CommandContext(ctx, c.cmd)
	}
	err := cmd.Run()
	if c.err != err {
		c.err = err
		return true
	}
	return false
}

func (c *binarySensorCmd) Publish() {
	c.ps.Publish(c.topic, c.msg)
}

func (c *binarySensorCmd) Refresh(forced bool) {
	if !c.update() && !forced {
		return
	}
	vv := []string{}
	if c.err == nil {
		vv = append(vv, `"state": "on"`)
	} else {
		vv = append(vv, `"state": "off"`)
		ec, ok := c.err.(*exec.ExitError)
		if ok {
			code := ec.ExitCode()
			if code != 1 {
				vv = append(vv, fmt.Sprintf(`"exit_code": "%d"`, code))
			}
		} else {
			vv = append(vv, fmt.Sprintf(`"error": "%s"`, c.err))
		}
	}
	c.msg = fmt.Sprintf("{%s}", strings.Join(vv, ", "))
	c.Publish()
}
