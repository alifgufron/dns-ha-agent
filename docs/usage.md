# Usage

## Build requirements

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go toolchain | see the `go` line in `go.mod` (currently **1.24+**) | The only build dependency |
| C compiler | not needed | `CGO_ENABLED=0` — the binary is pure Go and static |
| Third-party Go modules | none | Standard library only; no network access needed to build |
| Build host OS | any | Linux/macOS can cross-compile a FreeBSD binary |

Install the toolchain:

```bash
pkg install go          # FreeBSD
apt install golang-go   # Debian/Ubuntu (or grab a tarball from https://go.dev/dl/)
dnf install golang      # RHEL/Rocky
go version              # verify it meets the version in go.mod
```

`scripts/install.sh` checks both that `go` exists and that it is new enough,
and aborts with an explicit message if not.

### Runtime requirements (target node)

- FreeBSD 14.x/15.x, root access
- Base-system tools used by the agent: `ifconfig`, `sysctl`, `pgrep`
- Two interfaces: management (always up) + VIP/CARP (driven by the agent)
- No runtime library dependencies — the binary is static

## Build & Makefile

Using standard `make`:

```bash
make            # builds build/dns-ha-agent
make test       # runs unit test suite
make install    # installs binary, rc.d script, and sample config (requires root)
make clean      # cleans build/ artifacts
```

Or using `go build` directly:

```bash
# FreeBSD amd64
GOOS=freebsd GOARCH=amd64 go build -o build/dns-ha-agent-freebsd-amd64 ./cmd/dns-ha-agent

# FreeBSD arm64
GOOS=freebsd GOARCH=arm64 go build -o build/dns-ha-agent-freebsd-arm64 ./cmd/dns-ha-agent

# Validate config syntax
go run ./cmd/dns-ha-agent -t -config configs/config.yaml

# Run the test suite
go test ./...
```

`scripts/install.sh` also auto-builds and detects the platform (`GOOS`/`GOARCH`
from `uname`, overridable via env).

## CLI Diagnostics & Tools

The agent provides built-in interactive CLI tools for terminal diagnostics:

### 1. Health Check Report (`check`)
Runs a single comprehensive check cycle and renders an immediate summary report:

```bash
dns-ha-agent check
dns-ha-agent check -config /usr/local/etc/dns-ha-agent.yaml
```

Output:
```text
┌────────────────────────────────────────────────────────┐
│  DNS Health Check Report (14ms)                        │
├──────────────────────────┬────────┬────────────────────┤
│ Check                    │ Status │ Detail / Weight    │
├──────────────────────────┼────────┼────────────────────┤
│ Process (dnsdist)        │ OK     │ weight: 25         │
│ TCP :53                  │ OK     │ weight: 25         │
│ UDP :53                  │ OK     │ weight: 25         │
│ DNS Query (A)            │ OK     │ RTT: 12ms (wt: 25) │
├──────────────────────────┴────────┴────────────────────┤
│ Score: 100 / 100  (Raw: 100/100)   State: HEALTHY      │
└────────────────────────────────────────────────────────┘
```

### 2. Node & Peer Status Dashboard (`status`)
Inspects local CARP status and probes all configured cluster peers:

```bash
dns-ha-agent status
```

### 3. Version Info (`version`)
```bash
dns-ha-agent version
```

## Install

### Quick (Makefile)

```bash
make install          # build + install binary, config, rc.d (run as root / doas / sudo)
```

To remove:
```bash
make uninstall        # removes binary and rc.d script (preserves config)
make purge            # removes binary, rc.d script, config, log, and state
```

### Install without a Go toolchain

The target node does not need Go — build elsewhere and copy the binary over:

```bash
# On the build host
GOOS=freebsd GOARCH=amd64 make
scp build/dns-ha-agent root@node1:/tmp/

# On the FreeBSD node (see "Manual" below for config + rc.d)
install -m 0555 /tmp/dns-ha-agent /usr/local/bin/dns-ha-agent
```

### Manual

```bash
cp build/dns-ha-agent /usr/local/bin/dns-ha-agent && chmod 0555 /usr/local/bin/dns-ha-agent
cp configs/config.yaml /usr/local/etc/dns-ha-agent.yaml && chmod 0640 /usr/local/etc/dns-ha-agent.yaml
cp scripts/rc.d/dns-ha-agent /usr/local/etc/rc.d/ && chmod 0555 /usr/local/etc/rc.d/dns-ha-agent
```

### Secrets (NOT in the config file)

```bash
mkdir -p /etc/rc.conf.d
cat > /etc/rc.conf.d/dns-ha-agent << 'EOF'
export HA_TOKEN="RahasiaSuperAman123"
export SMTP_PASS="smtpsecret"
EOF
chmod 0600 /etc/rc.conf.d/dns-ha-agent
```

### Enable & start

```bash
sysrc dns_ha_agent_enable=YES
service dns-ha-agent start
service dns-ha-agent status
```

### rc.conf (CARP)

```bash
# vtnet0 — management
ifconfig_vtnet0="inet 172.16.10.100/25"
# vtnet1 — VIP/CARP (primary advskew 0; secondary advskew 100)
ifconfig_vtnet1="inet 172.16.10.10/25 vhid 1 advbase 1 advskew 0"
# dual-stack — inet6 alias, same VHID
ifconfig_vtnet1_ipv6="inet6 fd00:1::100/96 vhid 1 advbase 1 advskew 0"
defaultrouter="172.16.10.1"
```

### net.inet.carp.preempt (optional)

The agent reclaims MASTER on its own, so this sysctl can stay at its default (`0`).

If you do enable it on a node, set it **together with** a lower advskew — the
manpage requires both (a BACKUP vhid preempts a master announcing a *higher*
advskew):

```bash
# Node A only — kernel reclaims MASTER by itself
sysctl net.inet.carp.preempt=1
sysrc -f /etc/sysctl.conf net.inet.carp.preempt=1   # persist across reboots
# and in rc.conf, node A must have the lower advskew:
#   ifconfig_vtnet1="inet 172.16.10.10/25 vhid 1 advbase 1 advskew 0"
```

The agent detects this automatically (peers report `preempt` over the heartbeat)
and **defers to the kernel** instead of stepping down at the same time, falling
back after a 15s grace period if the kernel does not reclaim. No config change
needed. See architecture.md → "Kernel preempt interop".

### Log rotation (newsyslog)

```bash
cat > /etc/newsyslog.conf.d/dns-ha-agent.conf << 'EOF'
/var/log/dns-ha-agent.log   644  7     *    @T00   ZJ
EOF
```

---

## Verification

```bash
tail -f /var/log/dns-ha-agent.log
sysctl net.inet.carp.demotion        # 0=HEALTHY 50=DEGRADED 255=UNHEALTHY
ifconfig vtnet1 | grep carp          # carp: MASTER / BACKUP
curl -H "X-HA-DDIST-TOKEN: secret" http://10.0.0.11:8845/health
```

Reload without restart: `service dns-ha-agent reload` (SIGHUP). Changes to
health/weights/policy/notify apply immediately; peer `bind/port/token/tls` require
a restart.

---

## TLS Peer (optional)

```bash
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout /usr/local/etc/dns-ha-agent.key \
  -out    /usr/local/etc/dns-ha-agent.crt \
  -days 3650 -subj "/CN=10.0.0.1" \
  -addext "subjectAltName=IP:10.0.0.1"
chmod 0600 /usr/local/etc/dns-ha-agent.key
```

```yaml
peer:
  tls:
    enabled: true
    cert_file: "/usr/local/etc/dns-ha-agent.crt"
    key_file: "/usr/local/etc/dns-ha-agent.key"
```

Must be enabled on **all** nodes. Self-signed is fine (authentication is via the
token, not the certificate). After enabling: `service dns-ha-agent restart`, then
test `curl -k ... https://10.0.0.11:8845/health`.

---

## Multi-Node

All nodes use an identical config (`policy: preempt`). Only `peer.bind` and `peers` differ per node.

That includes `notify.vip_loss_alert: true` on **every** node — each agent
only watches its own VIP, so a node without it loses the VIP silently.

### 2 Nodes

```yaml
# Node A (10.0.0.11)
peer:
  enabled: true
  bind: "10.0.0.11"
  port: ":8845"              # MUST be identical on all nodes
  token: "${HA_TOKEN}"       # MUST be identical on all nodes
  peers:
    - ip: "10.0.0.12"
      name: "node-b"
policy:
  mode: "preempt"
```

```yaml
# Node B (10.0.0.12) — mirror, bind=10.0.0.12, peer=10.0.0.11
```

### 3 Nodes

Each node's `peers:` lists the other two; `bind` = own management IP.

### BIND9 / dnsdist+BIND9

```yaml
health:
  process_check: true
  process_names: ["named"]            # BIND9 only
  # process_names: ["dnsdist", "named"]   # dnsdist + BIND9 backend
  tcp_check: true
  udp_check: true
  dns_query:
    enabled: true
    domain: "internal.example.com"    # for BIND9: a zone only this server can resolve
    timeout: 2s
  bind_address: "127.0.0.1:53"
  weights:                            # optional — weight per check
    process: 20
    tcp: 20
    udp: 20
    dns: 40
```

---

## Failover (Summary)

| Scenario | N1 (advskew 0) | N2 (advskew 100) | Result |
|----------|----------------|------------------|--------|
| Both healthy | MASTER | BACKUP | **N1 MASTER** |
| N1 crash | `vtnet1 DOWN` | MASTER | **N2 MASTER** (link down) |
| N1 recovers | hold → UP → BACKUP | MASTER → step down | **N1 MASTER** (preempt, after recovery confirmation) |
| N2 crash | MASTER | `vtnet1 DOWN` | **N1 MASTER** |
| N2 recovers | MASTER | hold → UP → BACKUP | **N1 stays MASTER** |
| Both crash | `DOWN` | `DOWN` | **No MASTER** |
| N1 total failure | — | MASTER | **N2 MASTER** (timeout) |

### Recovery confirmation

A node coming back does **not** reclaim the VIP the moment one check passes.
It keeps its VIP interface down until every enabled check has passed for
`agent.recovery_confirm` consecutive intervals (default `3`):

```yaml
agent:
  recovery_confirm: 5      # 5 × interval — with interval 5s, ~25s of proven health
```

Why it exists: when `dnsdist` restarts, the process is alive seconds before
:53 actually answers. Without the wait the agent brought the interface straight
back up, and a kernel with `net.inet.carp.preempt=1` (or plain CARP priority)
handed the VIP back at score 25/100 — the node then flapped between DEGRADED and
HEALTHY while serving as MASTER.

Log line while waiting:

```
[CHECK HEALTH] ... state=UNHEALTHY decision=recovery-hold — 2/5 stable checks (score 75/100), demotion 255, vip_iface down
```

Notes:

- **Any** failed check resets the counter — a flapping node never creeps up to
  the threshold.
- While held, the node keeps demotion at the unhealthy level (255), not just the
  interface down. This is deliberate: peers decide to step down by comparing
  effective advskew (`advskew + demotion`), so a held node advertising demotion 0
  would make the healthy MASTER stand down for a node not ready to serve —
  leaving the VIP owned by nobody for a moment.
- The node reports `UNHEALTHY` while waiting, so the recovery email arrives when
  the VIP is genuinely reclaimed, not while it is still confirming.
- Only applies to `policy.mode: preempt`. Sticky never reclaims, so it is not
  held back. Failover (going down) is never delayed — only coming back is.
- `recovery_confirm: 0` disables the wait entirely (old behaviour).

---

## Notifications

3 alert types (state change, peer down/up, unexpected VIP loss) → all enabled
channels (email + Slack + Telegram). Per-key anti-spam cooldown, default 5m. Details: see architecture.md.

```yaml
notify:
  email:
    enabled: true
    smtp_host: "mail.example.com"
    smtp_port: 587
    username: "agent"
    password: "${SMTP_PASS}"
    from: "dns-ha@example.com"
    to: ["admin@example.com"]
  slack:                     # optional
    enabled: false
    webhook_url: "https://hooks.slack.com/services/..."
  telegram:                  # optional
    enabled: false
    bot_token: "${TELEGRAM_TOKEN}"
    chat_id: "123456789"     # negative for group/channel
  cooldown: 5m
  confirm: 3                     # consecutive checks a state must hold before the email fires
  vip_loss_alert: true
```

Emails include the predicted CARP state + the last 10 lines of `/var/log/messages` (`carp:`/`arp:`).

### `confirm`

A state-change email only fires after the new state has held for `confirm`
consecutive checks (default `3` — with a 5s interval that is ~10s). It filters
transient dips that resolve on their own, e.g. a `dnsdist` restart showing
`UNHEALTHY` (score 25) for one cycle before coming back, which used to generate
a false "UNHEALTHY" alert.

**It never delays failover.** The CARP decision (interface up/down + demotion)
runs on the first check of the new state; only the notification waits for
confirmation. So `confirm` trades a slightly later email for accuracy, at no
cost to availability.

### `cooldown`

Minimum interval between two notifications **of the same kind**, to stop a
flapping node from flooding your inbox. It is *not* a global mute: the cooldown
is tracked per event key, so unrelated alerts still get through immediately.

| Event | Cooldown key |
|-------|--------------|
| State change | the transition, e.g. `HEALTHY->DEGRADED` |
| Peer down/up | `peer:<peer_ip>:<status>` |
| Unexpected VIP loss | `vip-loss` |

So with `cooldown: 5m`, a node flapping `HEALTHY->DEGRADED` repeatedly sends one
alert per 5 minutes, but a `DEGRADED->UNHEALTHY` transition or a peer going down
during that window still alerts right away. Suppressed alerts are logged:

```
[NOTIFY] notification suppressed by cooldown transition=HEALTHY->DEGRADED
```

Lower it for faster re-alerting, raise it on links that flap. The timer lives in
memory, so a restart clears it.

### Peer alerts

When a peer's heartbeat fails, the agent probes it directly (ICMP ping, TCP 53,
UDP 53) before alerting, and classifies the failure:

| Status | Meaning |
|--------|---------|
| `DOWN (host unreachable)` | No ICMP reply and nothing answered on any port — VM down or network partition |
| `DEGRADED (agent down, DNS serving)` | Agent/health endpoint down, but DNS still answers queries |
| `CRITICAL (host up, DNS not serving)` | Host alive (ICMP or a probe answered) but DNS is gone — hung userland, or DNS down/filtered |

The ICMP probe runs on every heartbeat failure — there is no switch to disable
it. The email and the `[PEER] peer unreachable` log line both carry the
per-probe detail (`ping`, `http`, `tcp53`, `udp53`) plus the last known CARP
role the peer held, so a dead VM, a hung agent, and a stopped DNS server each
get a distinct, accurate message instead of a generic "unreachable".

### `vip_loss_alert`

Split-brain guard. It answers one question: *"I just lost the VIP — was anyone
entitled to take it?"* If nobody was, something took the VIP behind the agent's
back, and you get an alert.

**Set it on every node.** The name is role-neutral on purpose: every node runs
the identical config (see "Multi-Node"), and each agent only watches its own
VIP — whichever role it happens to hold. There is no such thing as "the MASTER
node's config" vs "the BACKUP node's config"; one file, same value, all nodes.
Enabling it on just one node leaves the other's VIP loss silent.

The old name `master_loss_alert` is still accepted as a legacy alias, so an
already-installed config keeps working after an upgrade. When both are present,
`vip_loss_alert` wins.

#### When it fires

All three conditions must hold:

1. This node was MASTER and is now BACKUP
2. This node is still HEALTHY (score >= 80)
3. **No** reachable, healthy peer has a lower effective advskew

Condition 3 is the actual test — the first two only establish that a healthy
node lost the VIP.

With N1 `advskew 0` and N2 `advskew 100` (N1 has priority), as seen from **N2**:

| Scenario | N2's CARP | N2 health | Entitled peer? | Alert |
|----------|-----------|-----------|----------------|-------|
| N1 recovers and reclaims the VIP | MASTER → BACKUP | healthy | yes — N1 effective `0` < `100` | silent (normal failover) |
| dnsdist on N2 dies | MASTER → BACKUP | unhealthy | — | silent (the state-change alert already covers it) |
| N2 keeps the VIP | MASTER | healthy | — | silent |
| **VIP vanishes for no reason** | MASTER → BACKUP | healthy | **no** | **ALERT** |

Only active when `policy.mode: preempt` (in `sticky` mode a node never yields,
so the check does not apply). Default `true`.

#### What a fired alert means

Something claimed the VIP without a valid failover. Usual causes:

- A rogue node using the same `vhid` on that segment — a forgotten VM, or a
  typo in another machine's `rc.conf`
- VHID or passphrase mismatch between nodes, causing CARP desync where both
  sides believe they are MASTER
- A switch dropping CARP multicast (VLAN / IGMP snooping), or PF blocking
  proto 112, so nodes stop hearing each other
- Someone ran `ifconfig <vip_iface> down` by hand

Where to start:

```bash
ifconfig <vip_iface>                 # local CARP state + advskew
tail -50 /var/log/messages | grep -i carp
arp -an | grep <VIP>                 # which MAC answers for the VIP now
tcpdump -ni <vip_iface> proto 112    # who is advertising, and with what vhid
```

The MAC in `arp -an` is the quickest tell: a CARP VIP should map to a
`00:00:5e:00:01:<vhid>` virtual MAC. A physical MAC there means a rogue host
took the address.

#### Known false positive

If a peer legitimately reclaims the VIP but its **heartbeat is unreachable**
from this node (management link down, firewall, agent stopped), the peer fails
the `ph.OK` check in condition 3, so the takeover looks unjustified and the
alert fires anyway.

That is still worth knowing about — it means the management path is broken — but
the cause is different from a real split-brain. Distinguish them by checking
whether the peer is simply unreachable:

```bash
curl -H "X-HA-DDIST-TOKEN: $HA_TOKEN" http://<peer_mgmt_ip>:8845/health
```

You will usually see the `Peer DOWN` alert next to the MASTER-loss one in that
case. A genuine split-brain has a reachable, healthy peer.

Turn the option off only if a known-odd network makes it chronically noisy —
it is the only signal you get when a VIP disappears without a valid failover.

---

## PF Firewall

```pf
ext_if = "vtnet0"
cluster_ports = "{ 8845 }"
cluster_peers = "{ 10.0.0.11 10.0.0.12 }"
pass in quick on $ext_if proto tcp from $cluster_peers to any port $cluster_ports
pass out quick on $ext_if proto tcp to $cluster_peers port $cluster_ports
block in log on $ext_if proto tcp to any port $cluster_ports
```

```bash
pfctl -f /etc/pf.conf
```

---

## Troubleshooting

| Symptom | Check |
|---------|-------|
| Cannot set demotion/iface | Must be root: `whoami` |
| Peer connection refused | `sockstat -4 -p 8845`, check PF rules |
| CARP state not readable | `ifconfig vtnet1 \| grep -i carp`, check interface name |
| rc.d status inaccurate | `cat /var/run/dns_ha_agent.pid` |
| Config error | `/usr/local/bin/dns-ha-agent -t -config /usr/local/etc/dns-ha-agent.yaml` |

---

## Uninstall

### Quick (script)

```bash
sh scripts/uninstall.sh           # stop + remove binary + rc.d script
PURGE=1 sh scripts/uninstall.sh   # also delete config, log, state files
```

Safe by default: the binary and rc.d script are removed, but the **config, log,
and state files are kept** unless `PURGE=1` is set. Re-running the script or
reinstalling later then picks up exactly where you left off. Set `PURGE=1` for
a clean sweep:

| Path | File | Removed without `PURGE` |
|------|------|--------------------------|
| `/usr/local/bin/dns-ha-agent` | binary | yes |
| `/usr/local/etc/rc.d/dns-ha-agent` | rc.d script | yes |
| `/usr/local/etc/dns-ha-agent.yaml` | config | only with `PURGE=1` |
| `/var/log/dns-ha-agent.log` | log | only with `PURGE=1` |
| `/var/db/dns-ha-agent.state` | state | only with `PURGE=1` |

The script also stops the service and clears `dns_ha_agent_enable`.

**Secrets** in `/etc/rc.conf.d/dns-ha-agent` are always left alone — the file
is inert once the service is disabled. Remove it manually if you want it gone:

```bash
rm /etc/rc.conf.d/dns-ha-agent
```

The paths above assume the default install + default config. If the deployed
config used custom `log_file` / `agent.state_file` paths, remove those files
manually after running the script.

### Manual

```bash
service dns-ha-agent stop
sysrc dns_ha_agent_enable=NO
rm /usr/local/etc/rc.d/dns-ha-agent
rm /usr/local/bin/dns-ha-agent
rm /usr/local/etc/dns-ha-agent.yaml
rm /var/log/dns-ha-agent.log
rm /var/db/dns-ha-agent.state
rm /etc/rc.conf.d/dns-ha-agent
```
