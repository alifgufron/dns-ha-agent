# Configuration

## Example

```yaml
log_file: /var/log/dns-ha-agent.log
log_level: info

agent:
  interval: 5s
  interface: "vtnet0"          # management — for node IP and peer comms
  vip_interface: "vtnet1"      # VIP/CARP — controlled up/down to trigger failover
  vhid: 1
  recovery_confirm: 5          # stable checks required before reclaiming the VIP
  state_file: "/var/db/dns-ha-agent.state"

health:
  process_check: true
  process_names: ["dnsdist"]   # or ["named"] for BIND9, or ["dnsdist","named"] both
  tcp_check: true
  udp_check: true
  dns_query:
    enabled: true
    domain: "google.com"
    timeout: 2s
  bind_address: "127.0.0.1:53" # change if the DNS server listens on a public IP
  weights:
    process: 25
    tcp: 25
    udp: 25
    dns: 25

carp:
  demotion_healthy: 0
  demotion_degraded: 50
  demotion_unhealthy: 255

peer:
  enabled: true
  bind: "10.0.0.1"                  # management IP — listen on this IP
  port: ":8845"                     # port — MUST be identical on all nodes
  token: "${HA_TOKEN}"              # token — MUST be identical on all nodes
  dns_port: "53"                    # peer DNS port for the fallback probe (default 53)
  tls:
    enabled: false
    cert_file: "/usr/local/etc/dns-ha-agent.crt"
    key_file: "/usr/local/etc/dns-ha-agent.key"
  peers:
    - ip: "10.0.0.2"
      name: "node-b"
      token: ""                     # optional pairwise secret for THIS peer only

policy:
  mode: "preempt"                   # "preempt" | "sticky". MUST be "preempt" on all nodes.

notify:
  email:
    enabled: true
    smtp_host: "mail.example.com"
    smtp_port: 587
    username: "notif"
    password: "${SMTP_PASS}"
    from: "dns-ha@example.com"
    to:
      - "admin@example.com"
  slack:
    enabled: false
    webhook_url: "https://hooks.slack.com/services/..."
  telegram:
    enabled: false
    bot_token: "${TELEGRAM_TOKEN}"
    chat_id: "123456789"
  cooldown: 5m
  confirm: 3                        # consecutive cycles a state must hold before the email fires
  vip_loss_alert: true
```

---

## Field Reference

| Field | Description |
|-------|-------------|
| `log_file` | Optional. Log file path. Empty = stdout. Example: `/var/log/dns-ha-agent.log` |
| `log_level` | `debug`, `info`, `warn`, `error` (default `info`) |
| `agent.interval` | Health check interval (5s, 10s, 30s) |
| `agent.interface` | Management interface — node IP, peer comms. Always UP. Typically `vtnet0` |
| `agent.vip_interface` | VIP/CARP interface — **UP** healthy, **DOWN** unhealthy (triggers failover). Typically `vtnet1` |
| `agent.vhid` | CARP VHID, must match `/etc/rc.conf` |
| `agent.recovery_confirm` | **Preempt mode only.** Consecutive intervals a recovered node must be *fully* healthy (every enabled check passing) before it reclaims the VIP. Default `3`. `0` disables the wait. See usage.md → "Recovery confirmation" |
| `agent.state_file` | Optional. Persists last state so a restart resumes cleanly (no stale-transition emails) |
| `health.process_check` | Check DNS process(es) via pgrep. Uses `process_names` |
| `health.process_names` | **List** of processes for `pgrep -x`. All must be alive. `["dnsdist"]`, `["named"]`, `["dnsdist","named"]`. Legacy single `process_name` also accepted |
| `health.tcp_check` | TCP port :53 connectivity |
| `health.udp_check` | UDP port :53 via DNS query. Uses `dns_query.domain`; only checks that a valid DNS response returns (rcode ignored), so authoritative-only servers pass |
| `health.dns_query` | DNS query check (`enabled`, `domain`, `domains`, `record_type`, `timeout`, `latency_threshold`) |
| `health.bind_address` | Where the DNS server listens (default `127.0.0.1:53`) |
| `health.weights` | Weight per check (`process`/`tcp`/`udp`/`dns`), default 25/25/25/25. 0 = no score (use `*_check` flags to fully disable). Weights need not sum to 100 — the score is normalized to 0-100 against the sum of enabled weights |
| `health.dns_query.domain` | Primary domain to query. For an authoritative-only server (BIND9 without recursion) use a zone it actually serves, otherwise the check gets REFUSED and fails |
| `health.dns_query.domains` | Optional list of domains to query (`["google.com", "example.com"]`). If provided, all domains are queried and average RTT is computed |
| `health.dns_query.record_type` | Optional DNS RR type to query (default `"A"`, supports `"AAAA"`, `"SOA"`, `"TXT"`, `"MX"`, `"NS"`, `"PTR"`, `"SRV"`, `"CNAME"`) |
| `health.dns_query.latency_threshold` | Optional SLA threshold (e.g. `300ms`). If query succeeds but RTT exceeds threshold, awards 50% DNS weight as a latency SLA penalty |
| `carp.demotion_healthy` | Demotion when HEALTHY (default 0) |
| `carp.demotion_degraded` | Demotion when DEGRADED (default 50) |
| `carp.demotion_unhealthy` | Demotion when UNHEALTHY (default 255) |
| `peer.enabled` | Enable peer communication |
| `peer.bind` | HTTP listen IP — own management IP. **Differs per node** |
| `peer.port` | Listen port, `:PORT` (e.g. `":8845"`). **MUST be identical on all nodes** |
| `peer.token` | Shared secret (`${ENV_VAR}` supported). **MUST be identical on all nodes** |
| `peer.tls` | TLS for peer HTTP (`enabled`, `cert_file`, `key_file`). All nodes need certs; peers query `https://` |
| `peer.peers` | Other nodes: `ip`, `name`, optional pairwise `token` (overrides global for this pair) |
| `peer.dns_port` | Peer's DNS port, probed (TCP+UDP) only when the heartbeat fails. Default `53` |
| `policy.mode` | `"preempt"` (default) or `"sticky"`. **MUST be `"preempt"`** for MASTER reclaim. `"sticky"` never steps down |
| `notify.email.*` | SMTP config (`enabled`, `smtp_host`, `smtp_port`, `username`, `password`, `from`, `to`) |
| `notify.slack` | Optional Slack webhook (`enabled`, `webhook_url`) |
| `notify.telegram` | Optional Telegram bot (`enabled`, `bot_token`, `chat_id`) |
| `notify.cooldown` | Minimum interval between notifications **of the same kind** (per-key: transition / `peer:<ip>:<status>` / `vip-loss`), not a global mute. Default `5m`. See usage.md |
| `notify.confirm` | Consecutive cycles a state must hold before a state-change notification fires. Debounces transient dips (e.g. a restart showing score 25 for one cycle); **does not** delay failover, which the CARP decision drives immediately. Default `3` (≈10s at a 5s interval), `0` = default |
| `notify.vip_loss_alert` | Alert if a node loses the VIP with no peer entitled to take it (split-brain guard). Role-neutral: same value on every node. Default `true`. (`master_loss_alert` still accepted as a legacy alias) |

---

## Secrets & Port

**`peer.token` and `peer.port` MUST be identical on all nodes.**

Store secrets in `/etc/rc.conf.d/dns-ha-agent` — **NOT** in the config file:

```bash
# /etc/rc.conf.d/dns-ha-agent
export HA_TOKEN="secrettoken"
export SMTP_PASS="smtpsecret"
chmod 0600 /etc/rc.conf.d/dns-ha-agent
```

The config references `${HA_TOKEN}`, expanded at runtime by the agent.

---

## VHID

VHID = CARP group number. One VHID = one VIP. Nodes in the same VHID compete for MASTER. Multi-group example:

```
VHID 1 — 10.0.0.100/25 (dnsdist)   → A advskew 0 = MASTER, B advskew 100 = BACKUP
VHID 2 — 10.0.0.200/25 (web)       → A advskew 100 = BACKUP, B advskew 0 = MASTER
```

Each VHID is one `ifconfig_xxx_alias<N>` entry in `/etc/rc.conf`:

```bash
# Node A (PRIMARY)
ifconfig_vtnet0="inet 172.16.10.100/25"
ifconfig_vtnet1="inet 172.16.10.10/25 vhid 1 advbase 1 advskew 0"

# Node B (SECONDARY) — different advskew
ifconfig_vtnet1="inet 172.16.10.10/25 vhid 1 advbase 1 advskew 100"
```

Multi-VHID / dual-stack — add aliases with the same VHID:

```bash
ifconfig_vtnet1_alias0="inet 172.16.10.10/25 vhid 1 advbase 1 advskew 0"
ifconfig_vtnet1_alias1="inet 172.16.10.20/25 vhid 2 advbase 1 advskew 100"
ifconfig_vtnet1_ipv6="inet6 fd00:1::100/96 vhid 1 advbase 1 advskew 0"
```

**Config `vhid:` must match `/etc/rc.conf`** (used by the agent for logging).

---

## advskew

Static priority per VHID in `/etc/rc.conf`. **Lower = higher priority** to become MASTER:

```
advskew 0   → highest priority (default MASTER)
advskew 100 → lower priority
advskew 254 → lowest possible (can still be MASTER)
```

Advertisement interval = `advbase + (advskew / 256)` seconds. Enables load balancing for multi-group setups (each group prefers a different node).

---

## Demotion Values

**Must be 0-255. Identical on all nodes.** Outside that range is rejected.

```yaml
carp:
  demotion_healthy: 0      # normal
  demotion_degraded: 50    # advskew +50
  demotion_unhealthy: 255  # cannot become MASTER
```

> **Secondary** — the primary failover is `ifconfig <vip_iface> down`. Demotion is supplementary info only.

```
effective advskew = configured advskew + demotion
effective ≤ 254 → eligible MASTER
effective ≥ 255 → never MASTER
```

| Node | advskew | demotion | effective | Can be MASTER? |
|------|---------|----------|-----------|----------------|
| A | 0 | 0 | 0 | ✅ |
| B | 100 | 0 | 100 | ✅ |
| A (DNS crash) | 0 | 255 | 255 | ❌ |
| B (DNS crash) | 100 | 255 | 355 | ❌ |

---

## Policy Modes

| Mode | Behavior | Use case |
|------|----------|----------|
| `preempt` | MASTER steps down (via agent) when peer is healthy | **REQUIRED.** Enables MASTER reclaim after recovery |
| `sticky` | MASTER stays MASTER regardless of peer health | Not recommended. PRIMARY cannot reclaim MASTER |

**All nodes MUST use `preempt`.** The MASTER brings its `vip_interface` DOWN when it detects a healthy peer with higher priority (lower advskew), causing CARP failover back to the PRIMARY.
