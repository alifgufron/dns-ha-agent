package agent

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/carp"
	"github.com/alifgufron/dns-ha-agent/internal/config"
	"github.com/alifgufron/dns-ha-agent/internal/health"
	"github.com/alifgufron/dns-ha-agent/internal/notify"
	"github.com/alifgufron/dns-ha-agent/internal/peer"
)

type Runner struct {
	cfgMu sync.RWMutex
	cfg   *config.Config
	log   *slog.Logger
	done  chan struct{}
	wg    sync.WaitGroup

	reloadCh chan *config.Config

	lastHealth    health.HealthResult
	lastState     State
	lastDemotion  int
	lastIfaceDown bool
	wasMaster     bool

	preemptCooldown    time.Time
	kernelPreemptSince time.Time // when we started waiting for a peer's kernel preempt
	recoveryStreak     int       // consecutive fully-healthy checks while holding the VIP down

	peers *peerTracker

	peerSrv  *peer.HeartbeatServer
	notifier *notify.EventDispatcher
	stateMu  sync.RWMutex
}

// peerDownThreshold consecutive unreachable checks before declaring a peer DOWN.
// With the default 5s interval + 3s heartbeat timeout, this is ~10s of confirmed
// RTO/timeout before an alert fires, avoiding false positives on a single blip.
const peerDownThreshold = 2

// preemptCooldownPeriod is the minimum gap between agent step-downs, to
// prevent the VIP interface from flapping.
const preemptCooldownPeriod = 60 * time.Second

// kernelPreemptGrace is how long we let a peer's net.inet.carp.preempt=1
// kernel reclaim the VIP before the agent steps down as a fallback. CARP
// takeover normally happens within 3 × advbase (~3s), so this leaves ample
// margin while still guaranteeing a reclaim if the kernel does not act.
const kernelPreemptGrace = 15 * time.Second

func weightOrDefault(configured, def int) int {
	if configured > 0 {
		return configured
	}
	return def
}

// demotionLevels maps carp.demotion_* config to policy inputs. A zero
// unhealthy/degraded value means the key was omitted, so fall back to the
// documented defaults (0 / 50 / 255) rather than disabling demotion entirely.
func demotionLevels(cfg *config.Config) DemotionLevels {
	d := DemotionLevels{
		Healthy:   cfg.CARP.DemotionHealthy,
		Degraded:  cfg.CARP.DemotionDegraded,
		Unhealthy: cfg.CARP.DemotionUnhealthy,
	}
	if d.Degraded == 0 {
		d.Degraded = 50
	}
	if d.Unhealthy == 0 {
		d.Unhealthy = 255
	}
	return d
}

func NewRunner(cfg *config.Config, log *slog.Logger) *Runner {
	r := &Runner{
		cfg:           cfg,
		log:           log,
		done:          make(chan struct{}),
		reloadCh:      make(chan *config.Config, 1),
		lastState:     StateHealthy,
		lastDemotion:  -1,
		lastIfaceDown: false,
	}

	// Restore last state from disk so a restart doesn't re-notify a stale transition.
	if cfg.Agent.StateFile != "" {
		if loaded, ok := loadState(cfg.Agent.StateFile); ok {
			r.lastState = loaded
			log.Info("[STATE] restored last state from disk", "state", loaded.String())
		}
	}

	healthFn := func() health.HealthResult {
		r.stateMu.RLock()
		defer r.stateMu.RUnlock()
		return r.lastHealth
	}
	peerTokens := make([]string, 0, len(cfg.Peer.Peers))
	for _, p := range cfg.Peer.Peers {
		peerTokens = append(peerTokens, p.Token)
	}

	r.peerSrv = peer.NewHeartbeatServer(
		cfg.Peer.Token,
		peerTokens,
		cfg.Agent.VIPInterface,
		cfg.Agent.VHID,
		healthFn,
		log,
	)

	note := notify.NewEventDispatcher(log, cfg.Notify.Cooldown)
	if cfg.Notify.Email.Enabled {
		emailNotifier := notify.NewEmailNotifier(
			cfg.Notify.Email.SMTPHost,
			cfg.Notify.Email.SMTPPort,
			cfg.Notify.Email.Username,
			cfg.Notify.Email.Password,
			cfg.Notify.Email.From,
			cfg.Notify.Email.To,
		)
		note.AddNotifier(emailNotifier)
	}
	if cfg.Notify.Slack.Enabled {
		note.AddNotifier(notify.NewSlackNotifier(cfg.Notify.Slack.WebhookURL))
	}
	if cfg.Notify.Telegram.Enabled {
		note.AddNotifier(notify.NewTelegramNotifier(cfg.Notify.Telegram.BotToken, cfg.Notify.Telegram.ChatID))
	}
	r.notifier = note

	return r
}

func (r *Runner) cfgValue() *config.Config {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.cfg
}

// Reload swaps the config live. Peer listen/token/TLS changes still require a
// restart; everything else (health, weights, policy, notify) applies next cycle.
func (r *Runner) Reload(newCfg *config.Config) {
	select {
	case r.reloadCh <- newCfg:
		r.log.Info("[AGENT] config reload scheduled")
	default:
		r.log.Warn("[AGENT] config reload dropped (previous not processed)")
	}
}

func (r *Runner) Run() error {
	cfg := r.cfgValue()
	if cfg.Peer.Enabled {
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			srv := &httpServer{
				addr:    cfg.Peer.ListenAddr(),
				handler: r.peerSrv.Handler(),
				log:     r.log,
				tls:     cfg.Peer.TLS,
			}
			r.log.Info("[PEER] starting peer heartbeat server", "bind", cfg.Peer.Bind, "tls", cfg.Peer.TLS.Enabled)
			if err := srv.ListenAndServe(); err != nil {
				r.log.Error("[PEER] heartbeat server error", "error", err)
			}
		}()
	}

	r.log.Info("[AGENT] runner started",
		"interval", cfg.Agent.Interval,
		"mgmt_iface", cfg.Agent.Interface,
		"vip_iface", cfg.Agent.VIPInterface,
		"vhid", cfg.Agent.VHID,
	)

	ticker := time.NewTicker(cfg.Agent.Interval)
	defer ticker.Stop()

	r.runOnce()

	for {
		select {
		case <-r.done:
			r.log.Info("[AGENT] runner stopped")
			return nil
		case <-ticker.C:
			r.runOnce()
		case newCfg := <-r.reloadCh:
			r.cfgMu.Lock()
			old := r.cfg
			r.cfg = newCfg
			r.cfgMu.Unlock()
			if old.Peer.Bind != newCfg.Peer.Bind || old.Peer.Port != newCfg.Peer.Port ||
				old.Peer.Token != newCfg.Peer.Token || old.Peer.TLS != newCfg.Peer.TLS {
				r.log.Warn("[AGENT] peer listen/token/TLS changed — restart required for these to take effect")
			}
			if old.Agent.Interval != newCfg.Agent.Interval {
				ticker.Reset(newCfg.Agent.Interval)
			}
			r.log.Info("[AGENT] config reloaded",
				"interval", newCfg.Agent.Interval,
				"policy_mode", newCfg.Policy.Mode,
			)
		}
	}
}

// Stop gracefully shuts down: restores VIP interface + demotion so the node
// returns to a clean state on the next start, persists state, then exits.
func (r *Runner) Stop() {
	cfg := r.cfgValue()
	if r.lastIfaceDown {
		r.log.Info("[IFACE] graceful shutdown — restoring VIP interface up")
		if err := carp.InterfaceUp(cfg.Agent.VIPInterface); err != nil {
			r.log.Error("[IFACE] failed to restore interface up", "interface", cfg.Agent.VIPInterface, "error", err)
		}
		if err := carp.SetDemotion(0); err != nil {
			r.log.Error("[IFACE] failed to reset demotion", "error", err)
		}
		r.lastIfaceDown = false
	}
	if cfg.Agent.StateFile != "" {
		saveState(cfg.Agent.StateFile, r.lastState)
	}
	close(r.done)
	r.wg.Wait()
}

func (r *Runner) runOnce() {
	cfg := r.cfgValue()

	healthCfg := health.ProcessConfig{
		ProcessNames: cfg.Health.ProcessList(),
		DNSEnabled:   cfg.Health.DNSQuery.Enabled,
		DNSDomain:    cfg.Health.DNSQuery.Domain,
		BindAddress:  cfg.Health.BindAddress,
		Timeout:      cfg.Health.DNSQuery.Timeout,
	}
	// Weights: configured value, else default, else 0 when check disabled.
	w := cfg.Health.Weights
	healthCfg.ProcessWeight = weightOrDefault(w.Process, health.DefaultWeights.Process)
	healthCfg.TCPWeight = weightOrDefault(w.TCP, health.DefaultWeights.TCP)
	healthCfg.UDPWeight = weightOrDefault(w.UDP, health.DefaultWeights.UDP)
	healthCfg.DNSWeight = weightOrDefault(w.DNS, health.DefaultWeights.DNS)
	if !cfg.Health.ProcessCheck {
		healthCfg.ProcessWeight = 0
	}
	if !cfg.Health.TCPCheck {
		healthCfg.TCPWeight = 0
	}
	if !cfg.Health.UDPCheck {
		healthCfg.UDPWeight = 0
	}

	healthResult := health.RunChecks(healthCfg)

	r.stateMu.Lock()
	r.lastHealth = healthResult
	r.stateMu.Unlock()

	currentState := StateFromScore(healthResult.Score)
	stateChanged := currentState.Transitioned(r.lastState)

	carpState, err := carp.ReadState(cfg.Agent.VIPInterface, cfg.Agent.VHID)
	if err != nil {
		r.log.Warn("[CARP] failed to read CARP state", "error", err)
		carpState = carp.StateUnknown
	}

	var peerHealths []peer.PeerHealth
	if cfg.Peer.Enabled {
		entries := make([]peer.PeerEntry, 0, len(cfg.Peer.Peers))
		for _, p := range cfg.Peer.Peers {
			entries = append(entries, peer.PeerEntry{IP: p.IP, Name: p.Name, Token: cfg.Peer.TokenFor(p)})
		}
		peerHealths = peer.CheckAllPeers(entries, peer.CheckOptions{
			Port:      cfg.Peer.PortNum(),
			Timeout:   3 * time.Second,
			Ping:      cfg.Peer.Ping,
			TLS:       cfg.Peer.TLS.Enabled,
			DNSPort:   cfg.Peer.DNSPort,
			DNSDomain: cfg.Health.DNSQuery.Domain,
		})
	}

	policyMode := ParsePolicyMode(cfg.Policy.Mode)
	decision := EvaluatePolicy(policyMode, healthResult.Score, carpState, peerHealths, demotionLevels(cfg))

	// Hold back a recovering node until it proves it is fully healthy, so the
	// VIP is not handed to a node whose DNS has only partially come back.
	recoveryConfirm := cfg.Agent.RecoveryConfirm
	if recoveryConfirm == 0 {
		recoveryConfirm = DefaultRecoveryConfirm
	}
	var recoveryHeld bool
	decision, r.recoveryStreak, recoveryHeld = applyRecoveryHold(
		policyMode, decision, r.lastIfaceDown,
		healthResult.RawScore, healthResult.MaxScore,
		recoveryConfirm, r.recoveryStreak, demotionLevels(cfg),
	)
	if recoveryHeld {
		// The node is not serving: report it as UNHEALTHY so the recovery
		// email arrives when the VIP is actually reclaimed, not while waiting.
		currentState = StateUnhealthy
		stateChanged = currentState.Transitioned(r.lastState)
	}

	vipIface := r.cfg.Agent.VIPInterface

	// Step 1: Interface up/down with correct demotion ordering.
	//
	// FreeBSD CARP kernel automatically adds 240 to demotion when the
	// VIP interface goes DOWN, and subtracts 240 when it comes UP.
	//
	// net.inet.carp.demotion is ADDITIVE — the written value is added to
	// the current factor. SetDemotion reads current and computes delta.
	//
	// To reach the desired final demotion:
	//   - Going DOWN: write demotion FIRST, then bring interface down
	//     (kernel adds 240 on top → total = desired + 240)
	//   - Going UP:   bring interface UP FIRST (kernel subtracts 240
	//     from current value), THEN write the final desired demotion
	//   - Demotion-only: write demotion as usual
	if decision.DesiredIfaceDown && !r.lastIfaceDown {
		if decision.DesiredDemotion != r.lastDemotion {
			r.log.Info("[STATE] changing demotion",
				"from", r.lastDemotion,
				"to", decision.DesiredDemotion,
				"reason", decision.Action,
			)
			if err := carp.SetDemotion(decision.DesiredDemotion); err != nil {
				r.log.Error("[STATE] failed to set demotion", "error", err)
			}
		}
		r.log.Info("[IFACE] bringing VIP interface down",
			"interface", vipIface,
			"reason", decision.Action,
		)
		if err := carp.InterfaceDown(vipIface); err != nil {
			r.log.Error("[IFACE] failed to bring VIP interface down", "interface", vipIface, "error", err)
		}
		r.lastIfaceDown = true
		r.lastDemotion = decision.DesiredDemotion
	} else if !decision.DesiredIfaceDown && r.lastIfaceDown {
		r.log.Info("[IFACE] bringing VIP interface up",
			"interface", vipIface,
			"reason", decision.Action,
		)
		if err := carp.InterfaceUp(vipIface); err != nil {
			r.log.Error("[IFACE] failed to bring VIP interface up", "interface", vipIface, "error", err)
		}
		if decision.DesiredDemotion != r.lastDemotion {
			r.log.Info("[STATE] changing demotion",
				"from", r.lastDemotion,
				"to", decision.DesiredDemotion,
				"reason", decision.Action,
			)
			if err := carp.SetDemotion(decision.DesiredDemotion); err != nil {
				r.log.Error("[STATE] failed to set demotion", "error", err)
			}
		}
		r.lastIfaceDown = false
		r.lastDemotion = decision.DesiredDemotion
	} else if decision.DesiredDemotion != r.lastDemotion {
		r.log.Info("[STATE] changing demotion",
			"from", r.lastDemotion,
			"to", decision.DesiredDemotion,
			"reason", decision.Action,
		)
		if err := carp.SetDemotion(decision.DesiredDemotion); err != nil {
			r.log.Error("[STATE] failed to set demotion", "error", err)
		}
		r.lastDemotion = decision.DesiredDemotion
	}

	// Read actual demotion from sysctl for accurate preempt/notification calculations
	actualDemotion := decision.DesiredDemotion
	if demotion, err := carp.GetDemotion(); err == nil {
		actualDemotion = demotion
	}

	// Step 2: Preempt step-down — compare effective advskew.
	// Only step down if we are MASTER and a peer has strictly lower effective
	// advskew (higher priority).
	//
	// Kernel preempt interop: if that peer runs net.inet.carp.preempt=1, the
	// FreeBSD kernel reclaims MASTER on its own (a BACKUP vhid preempts a
	// master announcing a higher advskew). Stepping down at the same moment
	// would be a duplicate action — it flaps our interface for nothing and
	// opens a window with no MASTER. So we defer to the kernel and only act
	// as a fallback if the kernel has not taken over within kernelPreemptGrace.
	if policyMode == PolicyPreempt && !decision.DesiredIfaceDown && carpState == carp.StateMaster && currentState == StateHealthy {
		localAdvskew, _ := carp.GetAdvskew(vipIface, cfg.Agent.VHID)
		awaitingKernel := false
		for _, ph := range peerHealths {
			if !ph.OK || ph.Score < 80 {
				continue
			}
			peerEffective := ph.Advskew + ph.Demotion
			myEffective := localAdvskew + actualDemotion
			if peerEffective >= myEffective {
				continue
			}
			if time.Since(r.preemptCooldown) <= preemptCooldownPeriod {
				continue
			}

			// Peer's kernel will do the reclaim itself — give it time first.
			if ph.Preempt == 1 {
				awaitingKernel = true
				if r.kernelPreemptSince.IsZero() {
					r.kernelPreemptSince = time.Now()
					r.log.Info("[PREEMPT] peer has kernel preempt enabled — deferring to kernel",
						"peer", ph.Name,
						"peer_effective", peerEffective,
						"my_effective", myEffective,
						"grace", kernelPreemptGrace,
					)
				}
				if time.Since(r.kernelPreemptSince) < kernelPreemptGrace {
					continue
				}
				r.log.Warn("[PREEMPT] kernel preempt did not reclaim within grace — falling back to agent step-down",
					"peer", ph.Name,
					"waited", time.Since(r.kernelPreemptSince).Round(time.Second),
				)
			}

			r.log.Info("[PREEMPT] stepping down — peer has higher priority",
				"interface", vipIface,
				"peer", ph.Name,
				"peer_effective", peerEffective,
				"my_effective", myEffective,
				"peer_kernel_preempt", ph.Preempt,
			)
			if err := carp.InterfaceDown(vipIface); err != nil {
				r.log.Error("[PREEMPT] failed to bring VIP interface down", "interface", vipIface, "error", err)
			}
			r.lastIfaceDown = true
			r.preemptCooldown = time.Now()
			awaitingKernel = false
			break
		}
		// Reset the grace timer once no peer is waiting on a kernel reclaim,
		// so a stale timer can never skip the grace period next time.
		if !awaitingKernel {
			r.kernelPreemptSince = time.Time{}
		}
	} else {
		// No longer MASTER (or not eligible) — the reclaim happened, reset the timer.
		r.kernelPreemptSince = time.Time{}
	}

	// Step 3: Refresh CARP state after interface changes for accurate notification
	notificationCarp := carpState
	if stateChanged || r.lastIfaceDown != decision.DesiredIfaceDown {
		newCarpState, err := carp.ReadState(r.cfg.Agent.VIPInterface, r.cfg.Agent.VHID)
		if err == nil {
			carpState = newCarpState
		}
	}
	notificationCarp = carpState

	// Step 4: Predict final CARP state for notification (preempt mode only).
	// When HEALTHY with interface UP and currently BACKUP, we WILL become
	// MASTER only if our effective advskew is strictly lower than every
	// healthy peer's. A peer with equal or lower effective keeps the role:
	// the agent steps down only when a peer has strictly lower effective
	// (see step 2), and with equal values the kernel keeps the current master.
	// Sticky mode never preempts, so it is skipped — predicting MASTER there
	// would report a state the agent never drives (e.g. a healthy sticky MASTER
	// peer with a lower-priority advskew stays MASTER).
	if policyMode == PolicyPreempt && currentState == StateHealthy && notificationCarp == carp.StateBackup {
		localAdvskew, err := carp.GetAdvskew(r.cfg.Agent.VIPInterface, r.cfg.Agent.VHID)
		if err == nil {
			myEffective := localAdvskew + actualDemotion
			willBeMaster := true
			for _, ph := range peerHealths {
				if ph.OK && ph.Score >= 80 && (ph.Advskew+ph.Demotion) <= myEffective {
					willBeMaster = false
					break
				}
			}
			if willBeMaster {
				notificationCarp = carp.StateMaster
			}
		}
	}

	logCarp := carpState.String()
	if notificationCarp != carpState {
		logCarp = notificationCarp.String() + " (predicted)"
	}
	r.log.Info("[CHECK HEALTH] health check complete",
		"score", healthResult.Score,
		"raw_score", healthResult.RawScore,
		"max_score", healthResult.MaxScore,
		"state", currentState.String(),
		"carp", logCarp,
		"demotion", actualDemotion,
		"process", healthResult.ProcessAlive,
		"tcp", healthResult.TCPAlive,
		"udp", healthResult.UDPAlive,
		"dns", healthResult.DNSAlive,
		"decision", decision.Action,
	)

	for _, ph := range peerHealths {
		if ph.OK {
			r.log.Info("[PEER] peer status",
				"peer", ph.Name,
				"score", ph.Score,
				"state", ph.State,
				"carp", ph.Carp,
			)
		} else {
			r.log.Warn("[PEER] peer unreachable",
				"peer", ph.Name,
				"error", ph.Error,
			)
		}
	}

	if r.cfg.Peer.Enabled {
		if r.peers == nil {
			r.peers = newPeerTracker(peerDownThreshold)
		}

		nodeIP, _ := carp.GetNodeIP(r.cfg.Agent.Interface)

		for _, ph := range peerHealths {
			if ph.OK && ph.Carp != "" {
				r.peers.rememberCarp(ph.IP, ph.Carp)
			}

			wentDown, cameUp := r.peers.Update(ph.IP, ph.OK)

			// Classify why the heartbeat failed. "DOWN (unreachable)" becomes
			// the old one-size-fits-all status; the probe result decides
			// between host-down, agent-only, and critical DNS outage.
			status := ph.Severity.String()
			if ph.Severity == peer.SeverityNone {
				status = "DOWN (unreachable)"
			}

			info := notify.PeerProbeInfo{
				Diagnosis:  ph.Diagnosis,
				LastCarp:   r.peers.LastCarp(ph.IP),
				AgentProbe: ph.AgentProbe.String(),
				TCP53:      ph.TCP53.String(),
				UDP53:      udpProbeString(ph.UDP53OK),
			}

			switch {
			case wentDown:
				r.log.Warn("[PEER] peer declared DOWN",
					"peer", ph.Name,
					"ip", ph.IP,
					"consecutive_failures", peerDownThreshold,
					"status", status,
					"diagnosis", ph.Diagnosis,
				)
				r.notifier.DispatchPeer(
					status,
					ph.Name,
					ph.IP,
					ph.Error,
					healthResult.Score,
					currentState.String(),
					notificationCarp.String(),
					nodeIP,
					info,
				)
			case cameUp:
				r.log.Info("[PEER] peer recovered",
					"peer", ph.Name,
					"ip", ph.IP,
				)
				r.notifier.DispatchPeer(
					"UP (recovered)",
					ph.Name,
					ph.IP,
					"",
					healthResult.Score,
					currentState.String(),
					notificationCarp.String(),
					nodeIP,
					notify.PeerProbeInfo{},
				)
			case !ph.OK && ph.Severity == peer.SeverityCritical:
				// A VIP held by a node that no longer answers DNS needs a human.
				// The tracker fires the initial alert once, so re-fire here every
				// cooldown period until the peer is reachable again — the shared
				// "peer:IP:<status>" key is what rate-limits it.
				r.notifier.DispatchPeer(
					status,
					ph.Name,
					ph.IP,
					ph.Error,
					healthResult.Score,
					currentState.String(),
					notificationCarp.String(),
					nodeIP,
					info,
				)
			}
		}
	}

	if stateChanged {
		nodeIP, _ := carp.GetNodeIP(cfg.Agent.Interface)
		vip, _ := carp.GetVIP(cfg.Agent.VIPInterface, cfg.Agent.VHID)
		r.notifier.Dispatch(
			currentState.String(),
			r.lastState.String(),
			healthResult.Score,
			decision.DesiredDemotion,
			nodeIP,
			vip,
			notificationCarp.String(),
			cfg.Agent.Interface,
			cfg.Agent.VIPInterface,
			healthResult.ProcessAlive,
			healthResult.TCPAlive,
			healthResult.UDPAlive,
			healthResult.DNSAlive,
		)
		r.lastState = currentState
		if cfg.Agent.StateFile != "" {
			saveState(cfg.Agent.StateFile, r.lastState)
		}
	}

	// Unexpected VIP loss detection (split-brain guard).
	// We were MASTER, are now BACKUP, but no healthy peer has higher priority
	// (lower effective advskew) to justify the step-down. This signals a problem:
	// e.g. a rogue node claiming the VIP, or the CARP state got out of sync.
	if cfg.Notify.VIPLossAlert && policyMode == PolicyPreempt &&
		r.wasMaster && notificationCarp == carp.StateBackup && currentState == StateHealthy {
		localAdvskew, _ := carp.GetAdvskew(cfg.Agent.VIPInterface, cfg.Agent.VHID)
		myEffective := localAdvskew + actualDemotion
		peerJustifiesMaster := false
		for _, ph := range peerHealths {
			if ph.OK && ph.Score >= 80 && (ph.Advskew+ph.Demotion) < myEffective {
				peerJustifiesMaster = true
				break
			}
		}
		if !peerJustifiesMaster {
			nodeIP, _ := carp.GetNodeIP(cfg.Agent.Interface)
			r.log.Warn("[STATE] unexpected VIP loss — healthy node lost VIP without a higher-priority peer",
				"carp", notificationCarp.String(),
				"my_effective", myEffective,
			)
			r.notifier.DispatchMasterLoss(
				"lost without higher-priority peer",
				healthResult.Score,
				notificationCarp.String(),
				nodeIP,
			)
		}
	}
	if currentState == StateHealthy {
		r.wasMaster = notificationCarp == carp.StateMaster
	}
}

// udpProbeString renders the UDP :53 result for the notification detail block.
func udpProbeString(ok bool) string {
	if ok {
		return "✓ answering DNS queries"
	}
	return "✗ not answering DNS queries"
}

type httpServer struct {
	addr    string
	handler http.Handler
	log     *slog.Logger
	tls     config.TLSServerConfig
}

func (s *httpServer) ListenAndServe() error {
	srv := &http.Server{
		Addr:    s.addr,
		Handler: s.handler,
	}
	if s.tls.Enabled {
		return srv.ListenAndServeTLS(s.tls.CertFile, s.tls.KeyFile)
	}
	return srv.ListenAndServe()
}
