// SPDX-FileCopyrightText: 2019 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"log"
	"time"
)

// Poller calls a function periodically, or when force refreshed.
type Poller struct {
	period  time.Duration
	refresh chan bool
	done    chan struct{}
	t       *time.Ticker
}

type pollerConfig struct {
	Period string
}

// NewPoller creates a Poller that will call the func periodically, or when force refreshed.
// The bool passed to the func indicates if the update was forced.
func NewPoller(cfg *pollerConfig, f func(bool)) *Poller {
	period, err := time.ParseDuration(cfg.Period)
	if err != nil {
		log.Fatalf("error parsing period '%s': %v", cfg.Period, err)
	}
	p := Poller{
		period:  period,
		refresh: make(chan bool),
		done:    make(chan struct{}),
	}
	go func() {
		for {
			select {
			case forced := <-p.refresh:
				f(forced)
			case <-p.done:
				return
			}
		}
	}()
	go func() {
		p.t = time.NewTicker(p.period)
		defer p.t.Stop()
		for {
			select {
			case <-p.t.C:
				select {
				case p.refresh <- false:
				case <-p.done:
					return
				}
			case <-p.done:
				return
			}
		}
	}()
	return &p
}

// Refresh triggers an immediate call of the polled function,
// with the forced parameter indicating if an update should be forced.
func (p *Poller) Refresh(forced bool) {
	select {
	case p.refresh <- forced:
	case <-p.done:
	}
}

// UpdatePeriod sets the update period for the Poller.
// Triggers an immediate unforced updated of the polled function
// before beginning the new update period.
func (p *Poller) UpdatePeriod(period time.Duration) {
	p.period = period
	select {
	case p.refresh <- false:
	case <-p.done:
		return
	}
	p.t.Reset(p.period)
}

// Close shuts down the Poller goroutines.
func (p *Poller) Close() {
	close(p.done)
}

// PolledSensor represents a sensor which is regularly polled.
type PolledSensor struct {
	topic  string
	poller *Poller
	ps     PubSub
}

// Close shuts down the polling of the sensor.
func (s *PolledSensor) Close() {
	if s == nil {
		return
	}
	s.poller.Close()
}

// Done returns true if the PolledSensor has been closed.
func (s *PolledSensor) Done() chan struct{} {
	return s.poller.done
}

// SetPollPeriod updates the polling period of the sensor.
func (s *PolledSensor) SetPollPeriod(b []byte) {
	d, err := time.ParseDuration(string(b))
	if err != nil {
		return
	}
	s.poller.UpdatePeriod(d)
	s.ps.Publish(s.topic+"/poll_period", d)
}

// Sync binds the PolledSensor to the PubSub.
// Its state remains synchronised until the PolledSensor is closed.
func (s *PolledSensor) Sync(ps PubSub) {
	if s == nil {
		return
	}
	s.ps = ps
	s.poller.Refresh(true)
	ps.Subscribe(s.topic+"/rqd", func([]byte) { s.poller.Refresh(true) })
	ps.Publish(s.topic+"/poll_period", s.poller.period)
	ps.Subscribe(s.topic+"/rqd/poll_period", s.SetPollPeriod)
}
