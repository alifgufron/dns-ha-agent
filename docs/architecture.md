# Architecture

## Dual-Interface Model

Each node requires 2 interfaces:

| Interface | Config field | Role | Status |
|-----------|-------------|------|--------|
| `vtnet0` | `interface` | Management: node IP, peer HTTP, SSH, monitoring | **Always UP** |
| `vtnet1` | `vip_interface` | VIP/CARP: carries the virtual DNS IP | **UP/DOWN by agent** |

**Why 2 interfaces?** When `vip_interface` is DOWN to trigger failover, the
management interface stays UP so the agent still reaches peers and runs health
checks via loopback (`127.0.0.1:53`).

---

## CARP Failover

### Primary: Interface Down

`ifconfig <vip_iface> down` when UNHEALTHY → link state change → FreeBSD CARP
kernel detects the interface went down → BACKUP takes over immediately (no
advertisement timeout wait).

```
HEALTHY:   iface UP   → CARP MASTER (or BACKUP per advskew)
UNHEALTHY: iface DOWN → MASTER → INIT (link down); peer BACKUP → MASTER (immediate)
RECOVERY:  iface UP   → INIT → BACKUP → MASTER (if peer steps down via preempt)
```

CARP transitions: `INIT ⇄ BACKUP ⇄ MASTER`. MASTER timeout = 3 × advbase (default 3s).

### Secondary: Demotion (sysctl)

`net.inet.carp.demotion` is **additive** — the written value is added to the current
factor. The agent reads the current value, computes `delta = target - current`, and
writes the delta. Demotion raises effective advskew but does **not** trigger a CARP
transition by itself.

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

---

## Runner Cycle (every interval)

1. **Health check** — pgrep `process_names` (one or more), TCP :53, UDP :53, DNS query → score = sum of passing weights (default 25/25/25/25, configurable via `health.weights`)
2. **Read CARP state** — `ifconfig <vip_iface>` → MASTER/BACKUP
3. **Peer check** — HTTP `/health` per peer → score, carp_state, advskew, demotion, preempt. Optional: pairwise token, TLS; ICMP ping on failure (host vs service classification)
4. **Policy (preempt)** — UNHEALTHY→demotion 255 + iface DOWN · DEGRADED→demotion 50 · HEALTHY→demotion 0
4b. **Recovery hold** (preempt only) — a node whose iface is DOWN keeps the unhealthy posture (demotion 255 + iface down) until `raw_score == max_score` for `agent.recovery_confirm` consecutive intervals, so a half-recovered node cannot reclaim the VIP. Holding demotion (not just the interface) stops healthy peers stepping down for it
5. **Apply demotion + interface** — critical ordering:
   - DOWN: SetDemotion(target) first, then iface down (kernel adds 240)
   - UP: iface up first (kernel subtracts 240), then SetDemotion(target)
6. **Preempt** — if MASTER + healthy peer + `peer_effective < my_effective` → step down via iface down (60s cooldown anti-flap). If that peer runs `net.inet.carp.preempt=1`, defer to its kernel first (15s grace, then fall back)
7. **Predict CARP** — if BACKUP but our effective < all healthy peers → predict MASTER (deterministic transition)
8. **Notify** — state change / peer down-up / unexpected VIP loss → email + Slack + Telegram
9. **Persist state** — save to `agent.state_file` on change

---

## State Machine

```
HEALTHY (≥80)    → demotion 0,  iface UP
DEGRADED (40-79) → demotion 50, iface UP
UNHEALTHY (<40)  → demotion 255, iface DOWN → triggers CARP failover
```

Check weights (default 25, set via `health.weights`): Process (`process_check`),
TCP (`tcp_check`), UDP (`udp_check`), DNS (`dns_query.enabled`). `process_names`
multi — **all** must be alive.

**Score is normalized to 0-100:** `score = raw / max × 100`, where `max` is the sum
of enabled weights. So a node whose every enabled check passes always reaches 100
and can be HEALTHY — even with `dns_query` disabled (max 75) or custom weights that
don't sum to 100. Logs show `score` (normalized), `raw_score`, and `max_score`.

---

## Agent Preempt

Agent-level preempt reclaims MASTER via interface down/up — reliable because it
is a link state change, unlike sysctl `net.inet.carp.preempt` which has proven
unreliable on some FreeBSD versions.

Heartbeat data exchanged per `/health`: `{score, carp_state, advskew, demotion,
preempt, timestamp}`.

Step-down only when `peer_effective (peer.advskew+peer.demotion) < my_effective`.

### Kernel preempt interop

`net.inet.carp.preempt=1` makes a **BACKUP** node preempt a master that
announces a *higher* advskew — the kernel does the reclaim itself. Note both
conditions are required: the sysctl **and** a lower advskew on that node.

If a node runs with the sysctl enabled, two mechanisms could fire at once
(kernel reclaims while the current MASTER also drops its interface), causing an
unnecessary flap and a brief window with no MASTER. The agent avoids this:

| Peer's `net.inet.carp.preempt` | Current MASTER's agent behavior |
|--------------------------------|----------------------------------|
| `0` (default) | Steps down immediately — the agent is the only mechanism |
| `1` | **Defers to the kernel.** If the kernel has not reclaimed within a 15s grace period, the agent steps down as a fallback |

So the sysctl stays the primary path when enabled, and the agent remains the
safety net if it fails. No configuration needed — the peer reports its `preempt`
value over the heartbeat and the agent adapts.

Mixed setups are fine: e.g. node A with `preempt=1` + `advskew 0`, node B with
the default `preempt=0` + `advskew 100`.

### Failover Flow

```
Normal:   A advskew 0 → MASTER · B advskew 100 → BACKUP
A crash:  A: UNHEALTHY → iface down → B: timeout → MASTER ✅
A recovers: A: iface up → INIT → BACKUP → (B steps down via preempt) → MASTER ✅
B step:   B: MASTER, peer A HEALTHY, peer_effective 0 < 100 → iface down → A timeout → MASTER
Total: A back as MASTER ~15s after DNS recovers.
```

---

## Notifications

### Channels (all receive the same subject+body)

| Channel | Config |
|---------|--------|
| Email | `notify.email` (SMTP STARTTLS) |
| Slack | `notify.slack` (webhook) |
| Telegram | `notify.telegram` (bot token + chat_id) |

### Alert Types

1. **State change** — HEALTHY↔DEGRADED↔UNHEALTHY
2. **Peer down/up** — peer unreachable (heartbeat timeout) 2× consecutively (~10s) → classified by the direct probes (ICMP + TCP 53 + UDP 53): ICMP/no reply → `DOWN (host unreachable)`; ICMP OK but DNS gone → `CRITICAL (host up, DNS not serving)`; DNS answering → `DEGRADED (agent down, DNS serving)`; back → `UP (recovered)`
3. **Unexpected VIP loss** (`notify.vip_loss_alert`) — node HEALTHY but lost the VIP (was MASTER, now BACKUP) without a higher-priority peer (split-brain / rogue node / CARP desync)

### Cooldown

Per-key, default 5m. Separate keys: each state transition, each peer+status, and `vip-loss`.

### Email Contents

Includes predicted CARP state (avoids misleading "CARP: BACKUP" when the node will deterministically become MASTER) + the last 10 lines of `/var/log/messages` matching `carp:`/`arp:`.

---

## Reliability

- **Graceful shutdown** (SIGTERM/SIGINT): restore iface UP + demotion 0 + persist state before exit
- **State persistence** (`agent.state_file`): restart does not re-notify stale transitions
- **Config reload** (SIGHUP / `service dns-ha-agent reload`): health/weights/policy/notify apply immediately; peer bind/port/token/tls require restart
- **Peer security**: shared-secret `X-DNS-HA-TOKEN` (or `Authorization: Bearer`), per-peer pairwise token, TLS (self-signed OK — the token is the authenticator, not the certificate)
- **Metrics exporter**: built-in `/metrics` OpenMetrics endpoint sharing the HTTP server for Prometheus scraping and Grafana dashboard visualization
