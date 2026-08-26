package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/alifgufron/dns-ha-agent/internal/agent"
	"github.com/alifgufron/dns-ha-agent/internal/carp"
	"github.com/alifgufron/dns-ha-agent/internal/config"
	"github.com/alifgufron/dns-ha-agent/internal/health"
	"github.com/alifgufron/dns-ha-agent/internal/logger"
	"github.com/alifgufron/dns-ha-agent/internal/peer"
)

const Version = "1.0.0"

func logToSyslog(tag, msg string) {
	exec.Command("logger", "-t", tag, msg).Run()
}

func printHelp() {
	fmt.Printf("DNS HA Agent v%s (FreeBSD CARP DNS Failover)\n\n", Version)
	fmt.Println("Usage:")
	fmt.Println("  dns-ha-agent [flags]             Run as background daemon")
	fmt.Println("  dns-ha-agent check [flags]       Run single health check cycle & print report")
	fmt.Println("  dns-ha-agent status [flags]      Show local CARP status & probe peers")
	fmt.Println("  dns-ha-agent version             Show version and runtime info")
	fmt.Println("\nFlags:")
	fmt.Println("  -config <path>   Path to config file (default: /usr/local/etc/dns-ha-agent.yaml)")
	fmt.Println("  -t               Test configuration syntax and exit")
	fmt.Println("  -h, --help       Show this help message")
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Printf("dns-ha-agent v%s (%s/%s, Go %s)\n", Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
			os.Exit(0)
		case "help", "-h", "--help":
			printHelp()
			os.Exit(0)
		case "check":
			runCheckCmd(os.Args[2:])
			return
		case "status":
			runStatusCmd(os.Args[2:])
			return
		default:
			// Skip flags like -config, -t which are handled by flag.Parse below
			if !strings.HasPrefix(os.Args[1], "-") {
				fmt.Fprintf(os.Stderr, "Error: unknown command %q\n\n", os.Args[1])
				printHelp()
				os.Exit(1)
			}
		}
	}

	configPath := flag.String("config", "/usr/local/etc/dns-ha-agent.yaml", "path to config file")
	checkOnly := flag.Bool("t", false, "test config and exit")
	flag.Parse()

	if *checkOnly {
		testConfig(*configPath)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		printConfigError(*configPath, err.Error())
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFile)
	log.Info("[AGENT] starting dns-ha-agent", "config", *configPath, "version", Version)

	runner := agent.NewRunner(cfg, log.Logger)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	go func() {
		for sig := range sigCh {
			if sig == syscall.SIGHUP {
				newCfg, err := config.Load(*configPath)
				if err != nil {
					log.Error("config reload failed — keeping current config", "error", err)
					continue
				}
				log.Info("[AGENT] SIGHUP received, reloading config")
				runner.Reload(newCfg)
				continue
			}
			log.Info("shutting down", "signal", sig)
			runner.Stop()
			return
		}
	}()

	if err := runner.Run(); err != nil {
		log.Fatal("runner exited with error", "error", err)
	}
}

func testConfig(path string) {
	errs := config.CheckConfig(path)
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  ╔══════════════════════════════════════════════════╗\n")
		fmt.Fprintf(os.Stderr, "  ║       DNS-HA-AGENT — CONFIG ERROR                ║\n")
		fmt.Fprintf(os.Stderr, "  ╚══════════════════════════════════════════════════╝\n\n")
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  • %s\n", e)
			logToSyslog("dns-ha-agent", "config error: "+e)
		}
		fmt.Fprintf(os.Stderr, "\n  Fix the config and try again.\n\n")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stdout, "  Config OK: %s\n", path)
	os.Exit(0)
}

func printConfigError(path, errMsg string) {
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  ╔══════════════════════════════════════════════════╗\n")
	fmt.Fprintf(os.Stderr, "  ║       DNS-HA-AGENT — CONFIG ERROR                ║\n")
	fmt.Fprintf(os.Stderr, "  ╚══════════════════════════════════════════════════╝\n\n")
	fmt.Fprintf(os.Stderr, "  File: %s\n", path)
	fmt.Fprintf(os.Stderr, "  %s\n", errMsg)
	logToSyslog("dns-ha-agent", fmt.Sprintf("config error: %s", errMsg))
	fmt.Fprintf(os.Stderr, "\n  Fix the config and try again.\n\n")
}

func runCheckCmd(args []string) {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	configPath := fs.String("config", "/usr/local/etc/dns-ha-agent.yaml", "path to config file")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		printConfigError(*configPath, err.Error())
		os.Exit(1)
	}

	w := cfg.Health.Weights
	healthCfg := health.ProcessConfig{
		ProcessNames:        cfg.Health.ProcessList(),
		ProcessWeight:       weightOrDefault(w.Process, health.DefaultWeights.Process),
		TCPWeight:           weightOrDefault(w.TCP, health.DefaultWeights.TCP),
		UDPWeight:           weightOrDefault(w.UDP, health.DefaultWeights.UDP),
		DNSWeight:           weightOrDefault(w.DNS, health.DefaultWeights.DNS),
		DNSEnabled:          cfg.Health.DNSQuery.Enabled,
		DNSDomain:           cfg.Health.DNSQuery.Domain,
		DNSDomains:          cfg.Health.DNSQuery.DomainList(),
		DNSRecordType:       cfg.Health.DNSQuery.Type(),
		DNSLatencyThreshold: cfg.Health.DNSQuery.LatencyThreshold,
		BindAddress:         cfg.Health.BindAddress,
		BindAddresses:       cfg.Health.BindAddressList(),
		Timeout:             cfg.Health.DNSQuery.Timeout,
	}
	if !cfg.Health.ProcessCheck {
		healthCfg.ProcessWeight = 0
	}
	if !cfg.Health.TCPCheck {
		healthCfg.TCPWeight = 0
	}
	if !cfg.Health.UDPCheck {
		healthCfg.UDPWeight = 0
	}

	fmt.Println("Running local DNS health checks...")
	start := time.Now()
	res := health.RunChecks(healthCfg)
	elapsed := time.Since(start)

	state := agent.StateFromScore(res.Score)

	// Build all row data first so we can calculate column widths dynamically
	type row struct {
		check  string
		status string
		detail string
	}

	dnsDetail := fmt.Sprintf("weight: %d", healthCfg.DNSWeight)
	if healthCfg.DNSEnabled {
		if res.DNSDetail.Slow {
			dnsDetail = fmt.Sprintf("SLOW: %v (weight: %d/2)", res.DNSDetail.RTT.Round(time.Millisecond), healthCfg.DNSWeight)
		} else if res.DNSAlive {
			dnsDetail = fmt.Sprintf("RTT: %v (weight: %d)", res.DNSDetail.RTT.Round(time.Millisecond), healthCfg.DNSWeight)
		} else if res.DNSDetail.Error != "" {
			dnsDetail = res.DNSDetail.Error
		}
	} else {
		dnsDetail = "disabled"
	}

	rows := []row{
		{"Process (" + strings.Join(healthCfg.ProcessNames, ",") + ")", sym(res.ProcessAlive), fmt.Sprintf("weight: %d", healthCfg.ProcessWeight)},
		{"TCP :53", sym(res.TCPAlive), fmt.Sprintf("weight: %d", healthCfg.TCPWeight)},
		{"UDP :53", sym(res.UDPAlive), fmt.Sprintf("weight: %d", healthCfg.UDPWeight)},
		{"DNS Query (" + healthCfg.DNSRecordType + ")", sym(res.DNSAlive), dnsDetail},
	}

	// Calculate minimum column widths from headers
	col1 := len("Check")
	col2 := len("Status")
	col3 := len("Detail / Weight")
	for _, r := range rows {
		if len(r.check) > col1 {
			col1 = len(r.check)
		}
		if len(r.status) > col2 {
			col2 = len(r.status)
		}
		if len(r.detail) > col3 {
			col3 = len(r.detail)
		}
	}

	// Total inner width = col1 + col2 + col3 + separators (" │ " = 3 each, 2 of them = 6) + 2 outer padding
	innerWidth := col1 + col2 + col3 + 10 // 2(pad) + 3(sep) + 3(sep) + 2(pad)

	// Build the summary line and ensure the table is wide enough for it
	summaryContent := fmt.Sprintf("Score: %-3d / 100  (Raw: %d/%d)   State: %s", res.Score, res.RawScore, res.MaxScore, state.String())
	if len(summaryContent)+2 > innerWidth { // +2 for left/right padding
		innerWidth = len(summaryContent) + 2
	}

	// Build the header line and ensure the table is wide enough for it
	headerContent := fmt.Sprintf("DNS Health Check Report (%s)", elapsed.Round(time.Millisecond))
	if len(headerContent)+4 > innerWidth { // +4 for extra padding
		innerWidth = len(headerContent) + 4
	}

	// Recalculate col3 to fill remaining space
	col3 = innerWidth - col1 - col2 - 10
	if col3 < len("Detail / Weight") {
		col3 = len("Detail / Weight")
		innerWidth = col1 + col2 + col3 + 10
	}

	hLine := strings.Repeat("─", innerWidth)
	fmt.Printf("\n┌%s┐\n", hLine)
	fmt.Printf("│  %-*s  │\n", innerWidth-4, headerContent)
	fmt.Printf("├%s┼%s┼%s┤\n", strings.Repeat("─", col1+2), strings.Repeat("─", col2+2), strings.Repeat("─", col3+2))
	fmt.Printf("│ %-*s │ %-*s │ %-*s │\n", col1, "Check", col2, "Status", col3, "Detail / Weight")
	fmt.Printf("├%s┼%s┼%s┤\n", strings.Repeat("─", col1+2), strings.Repeat("─", col2+2), strings.Repeat("─", col3+2))
	for _, r := range rows {
		fmt.Printf("│ %-*s │ %-*s │ %-*s │\n", col1, r.check, col2, r.status, col3, r.detail)
	}
	fmt.Printf("├%s┴%s┴%s┤\n", strings.Repeat("─", col1+2), strings.Repeat("─", col2+2), strings.Repeat("─", col3+2))
	fmt.Printf("│ %-*s │\n", innerWidth-4, summaryContent)
	fmt.Printf("└%s┘\n\n", hLine)
}

func runStatusCmd(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	configPath := fs.String("config", "/usr/local/etc/dns-ha-agent.yaml", "path to config file")
	fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		printConfigError(*configPath, err.Error())
		os.Exit(1)
	}

	fmt.Print("Collecting local node & peer status...\n\n")

	nodeIP, _ := carp.GetNodeIP(cfg.Agent.Interface)
	vip, _ := carp.GetVIP(cfg.Agent.VIPInterface, cfg.Agent.VHID)
	carpState, err := carp.ReadState(cfg.Agent.VIPInterface, cfg.Agent.VHID)
	if err != nil {
		carpState = carp.StateUnknown
	}
	advskew, _ := carp.GetAdvskew(cfg.Agent.VIPInterface, cfg.Agent.VHID)
	demotion, _ := carp.GetDemotion()
	preempt, _ := carp.GetPreempt()

	fmt.Println("── Local Node ────────────────────────────────────────────")
	fmt.Printf("  Hostname:        %s\n", getHostname())
	fmt.Printf("  Management IP:   %s (%s)\n", nodeIP, cfg.Agent.Interface)
	fmt.Printf("  CARP Interface:  %s (VHID: %d)\n", cfg.Agent.VIPInterface, cfg.Agent.VHID)
	fmt.Printf("  CARP Role:       %s (Advskew: %d, Demotion: %d, Preempt: %d)\n", carpState.String(), advskew, demotion, preempt)
	fmt.Printf("  Virtual IP:      %s\n", vip)
	fmt.Printf("  Policy Mode:     %s\n", cfg.Policy.Mode)

	if cfg.Peer.Enabled && len(cfg.Peer.Peers) > 0 {
		fmt.Println("\n── Peers ─────────────────────────────────────────────────")
		entries := make([]peer.PeerEntry, 0, len(cfg.Peer.Peers))
		for _, p := range cfg.Peer.Peers {
			entries = append(entries, peer.PeerEntry{IP: p.IP, Name: p.Name, Token: cfg.Peer.TokenFor(p)})
		}
		results := peer.CheckAllPeers(entries, peer.CheckOptions{
			Port:      cfg.Peer.PortNum(),
			Timeout:   3 * time.Second,
			Ping:      cfg.Peer.Ping,
			TLS:       cfg.Peer.TLS.Enabled,
			DNSPort:   cfg.Peer.DNSPort,
			DNSDomain: cfg.Health.DNSQuery.Domain,
		})

		for _, ph := range results {
			status := "ONLINE"
			if !ph.OK {
				status = "OFFLINE"
			}
			fmt.Printf("  • %-12s (%-15s) => %-7s", ph.Name, ph.IP, status)
			if ph.OK {
				fmt.Printf(" [Score: %3d/100, CARP: %-6s, Skew: %d, Demotion: %d]\n", ph.Score, ph.Carp, ph.Advskew, ph.Demotion)
			} else {
				diag := ph.Diagnosis
				if diag == "" {
					diag = ph.Error
				}
				fmt.Printf(" [%s]\n", diag)
			}
		}
	}
	fmt.Println()
}

func getHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func sym(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func weightOrDefault(configured, def int) int {
	if configured > 0 {
		return configured
	}
	return def
}
