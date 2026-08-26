# DNS HA Agent

Go agent for high-availability DNS services (dnsdist, BIND9/named, or any other
DNS server) on **FreeBSD only**, using CARP interface control.

> **Platform:** FreeBSD only. Depends on FreeBSD kernel features (`ifconfig` CARP
> states, `net.inet.carp.demotion`). Does not run on Linux/other OSes.

**Problem solved:** plain CARP only fails over on host-level failure (advertisement
timeout). This agent adds **service-level failover** — if the DNS service is
unhealthy (process dead, port :53 down, DNS not resolving), the agent brings the
CARP VIP interface DOWN, triggering immediate takeover by a healthy peer.

## How It Works (per cycle, default 5s)

1. **Health checks** — pgrep (one or more names via `process_names`), TCP :53, UDP :53, DNS query → weighted score 0-100 (`health.weights`)
2. **Read CARP state** — `ifconfig <vip_iface>` → MASTER / BACKUP
3. **Peer heartbeat** — HTTP `/health` (token auth), optional TLS + pairwise token; optional ICMP ping on failure (distinguishes "VM dead" vs "service down")
4. **Policy (preempt)** — UNHEALTHY(<40)→demotion 255 + iface DOWN · DEGRADED(40-79)→demotion 50 · HEALTHY(≥80)→demotion 0
5. **Apply** — fixed ordering: DOWN writes demotion first then iface down; UP brings iface up first then demotion
6. **Preempt** — MASTER steps down (`ifconfig down`) when a healthy peer has higher priority (lower effective advskew); if that peer has `net.inet.carp.preempt=1` the kernel reclaims first, agent falls back after 15s
7. **Notify** — state change, peer down/up, unexpected MASTER loss (split-brain) → email / Slack / Telegram, anti-spam cooldown

## Key Features

- Dual-interface (mgmt always UP + VIP/CARP controlled by agent) & dual-stack (IPv4+IPv6)
- Multi-DNS: `process_names` for dnsdist, BIND9, or both; configurable check weights
- **SLA-Aware Health Checks:** dynamic record types (A, AAAA, SOA, etc.), multi-domain queries, dual-stack IPv4/IPv6 `bind_addresses`, and latency SLA threshold penalty
- **OpenMetrics / Prometheus & Telegraf:** built-in `/metrics` exporter compatible with Prometheus, Telegraf (InfluxDB v1/v2), VictoriaMetrics, and ready-to-import Grafana dashboard template
- **CLI Diagnostics:** built-in `check` report, `status` dashboard, and `version` subcommands
- Primary failover via `ifconfig down` (<1s), agent-level preempt, effective advskew comparison
- Kernel-preempt aware: honors `net.inet.carp.preempt=1` on a peer instead of double-acting
- Peer: shared-secret, pairwise token, TLS, ping classification
- Notifications: email SMTP STARTTLS + Slack + Telegram; peer down/up & split-brain alerts
- State persistence (no stale emails after restart), SIGHUP config reload, graceful shutdown
- Only 2 external deps (`yaml.v3`, `miekg/dns`)

## Quick Start

```bash
# Build & install via Makefile
make
make install

# Enable and start the service
sysrc dns_ha_agent_enable=YES
service dns-ha-agent start

# Run CLI health check & status
dns-ha-agent check
dns-ha-agent status
```

## Prerequisites

**To build** — Go only (version per `go.mod`, currently 1.24+): `pkg install go`.
No cgo, no C compiler, no third-party modules. Any OS can cross-compile for
FreeBSD; the target node does not need Go if you copy the binary over.

**To run** — FreeBSD 14.x/15.x:

- root access; base tools `ifconfig`, `sysctl`, `pgrep`
- 2 interfaces: management + VIP/CARP
- CARP configured in rc.conf (`vhid`, `advskew` — see docs)

## Documentation

| Doc | Contents |
|-----|----------|
| [config.md](docs/config.md) | All config fields, VHID, advskew, demotion, policy |
| [usage.md](docs/usage.md) | Build, install, verify, multi-node examples, TLS, troubleshooting |
| [architecture.md](docs/architecture.md) | Architecture, CARP failover, runner cycle, notifications |

## Layout

```
cmd/dns-ha-agent/        entry point
internal/
  agent/                 runner, state machine, policy
  carp/                  CARP state, demotion, interface control
  config/                load, parse, validate
  health/                process, TCP, UDP, DNS checks
  logger/                structured logging
  notify/                email, slack, telegram, template, cooldown
  peer/                  HTTP server + heartbeat client
  util/                  exec, ping helpers
configs/config.yaml      example config
docs/                    architecture, config, usage
scripts/                 rc.d service script
Makefile                 FreeBSD build & install
build/                   compiled binaries (gitignored)
```
