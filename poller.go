// SPDX-FileCopyrightText: 2019 Kent Gibson <warthog618@gmail.com>
//
// SPDX-License-Identifier: MIT

package main

import (
	"time"
)

type Poller struct {
	period  time.Duration
	refresh chan bool
	done    chan struct{}
	t       *time.Ticker
}

type PollerConfig struct {
	Period time.Duration
}

func NewPoller(period time.Duration, f func(bool)) *Poller {
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

func (p *Poller) Refresh() {
	select {
	case p.refresh <- true:
	case <-p.done:
	}
}

func (p *Poller) UpdatePeriod(period time.Duration) {
	p.period = period
	select {
	case p.refresh <- false:
	case <-p.done:
		return
	}
	p.t.Reset(p.period)
}

func (p *Poller) Close() {
	close(p.done)
}

// PolledSensor represents a sensor which is regularly polled.
type PolledSensor struct {
	topic  string
	poller *Poller
	ps     PubSub
}

func (s *PolledSensor) Close() {
	if s == nil {
		return
	}
	s.poller.Close()
}

func (s *PolledSensor) Done() chan struct{} {
	return s.poller.done
}

func (s *PolledSensor) SetPollPeriod(b []byte) {
	d, err := time.ParseDuration(string(b))
	if err != nil {
		return
	}
	s.poller.UpdatePeriod(d)
	s.ps.Publish(s.topic+"/poll_period", d)
}

func (s *PolledSensor) Sync(ps PubSub) {
	if s == nil {
		return
	}
	s.ps = ps
	s.poller.Refresh()
	ps.Subscribe(s.topic+"/rqd", func([]byte) { s.poller.Refresh() })
	ps.Publish(s.topic+"/poll_period", s.poller.period)
	ps.Subscribe(s.topic+"/rqd/poll_period", s.SetPollPeriod)
}
