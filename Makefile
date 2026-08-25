# Makefile for dns-ha-agent (FreeBSD CARP DNS Failover)

PREFIX  ?= /usr/local
BINDIR  ?= $(PREFIX)/bin
CONFDIR ?= $(PREFIX)/etc
RCDIR   ?= $(PREFIX)/etc/rc.d
GO      ?= go
BINNAME  = dns-ha-agent

all: build

build:
	mkdir -p build
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o build/$(BINNAME) ./cmd/dns-ha-agent

test:
	$(GO) test -v ./...

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 0555 build/$(BINNAME) $(DESTDIR)$(BINDIR)/$(BINNAME)
	install -d $(DESTDIR)$(RCDIR)
	install -m 0555 scripts/rc.d/dns-ha-agent $(DESTDIR)$(RCDIR)/dns-ha-agent
	install -d $(DESTDIR)$(CONFDIR)
	@if [ ! -f $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml ]; then \
		install -m 0640 configs/config.yaml $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml; \
		echo "=> Installed default config to $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml"; \
	else \
		echo "=> Existing config $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml preserved"; \
	fi
	install -m 0640 configs/config.yaml $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml.sample
	@echo ""
	@echo "Installation complete."
	@echo "Next steps:"
	@echo "  1. Edit $(CONFDIR)/dns-ha-agent.yaml"
	@echo "  2. Enable service: sysrc dns_ha_agent_enable=YES"
	@echo "  3. Start service:  service dns-ha-agent start"
	@echo "  4. Check status:   dns-ha-agent status"

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINNAME)
	rm -f $(DESTDIR)$(RCDIR)/dns-ha-agent
	rm -f $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml.sample
	@echo "Uninstalled binary and service script."
	@echo "Config preserved at $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml (use 'make purge' to remove config, log, and state files)."

purge: uninstall
	rm -f $(DESTDIR)$(CONFDIR)/dns-ha-agent.yaml
	rm -f $(DESTDIR)/var/log/dns-ha-agent.log
	rm -f $(DESTDIR)/var/db/dns-ha-agent.state
	@echo "Purged config, log, and state files."

clean:
	rm -rf build

.PHONY: all build test install uninstall purge clean

