# SPDX-FileCopyrightText: 2019 Kent Gibson <warthog618@gmail.com>
#
# SPDX-License-Identifier: MIT

GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean

VERSION ?= $(shell git describe --tags --always --dirty 2> /dev/null )
LDFLAGS=-ldflags "-s -w -X=main.version=$(VERSION)"

all: dunnart

dunnart : % : %.go
	$(GOBUILD) $(LDFLAGS)

clean:
	$(GOCLEAN) ./...

pi0:
	GOARCH=arm GOARM=6 $(GOBUILD) $(LDFLAGS)

pi2:
	GOARCH=arm GOARM=7 $(GOBUILD) $(LDFLAGS)

mips:
	GOARCH=mips GOMIPS=softfloat $(GOBUILD) $(LDFLAGS)

mipsle:
	GOARCH=mipsle GOMIPS=softfloat $(GOBUILD) $(LDFLAGS)
