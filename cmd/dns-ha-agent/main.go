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

const Version = "1.1.0"

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

	fmt.Println("\n┌────────────────────────────────────────────────────────┐")
	fmt.Printf("│  DNS Health Check Report (%-28s) │\n", elapsed.Round(time.Millisecond))
	fmt.Println("├──────────────────────────┬────────┬────────────────────┤")
	fmt.Printf("│ %-24s │ %-6s │ %-18s │\n", "Check", "Status", "Detail / Weight")
	fmt.Println("├──────────────────────────┼────────┼────────────────────┤")
	fmt.Printf("│ %-24s │ %-6s │ %-18s │\n", "Process ("+strings.Join(healthCfg.ProcessNames, ",")+")", sym(res.ProcessAlive), fmt.Sprintf("weight: %d", healthCfg.ProcessWeight))
	fmt.Printf("│ %-24s │ %-6s │ %-18s │\n", "TCP :53", sym(res.TCPAlive), fmt.Sprintf("weight: %d", healthCfg.TCPWeight))
	fmt.Printf("│ %-24s │ %-6s │ %-18s │\n", "UDP :53", sym(res.UDPAlive), fmt.Sprintf("weight: %d", healthCfg.UDPWeight))

	dnsDetail := fmt.Sprintf("weight: %d", healthCfg.DNSWeight)
	if healthCfg.DNSEnabled {
		if res.DNSDetail.Slow {
			dnsDetail = fmt.Sprintf("SLOW: %v (weight: %d/2)", res.DNSDetail.RTT.Round(time.Millisecond), healthCfg.DNSWeight)
		} else if res.DNSAlive {
			dnsDetail = fmt.Sprintf("RTT: %v (weight: %d)", res.DNSDetail.RTT.Round(time.Millisecond), healthCfg.DNSWeight)
		} else if res.DNSDetail.Error != "" {
			dnsDetail = res.DNSDetail.Error
			if len(dnsDetail) > 18 {
				dnsDetail = dnsDetail[:15] + "..."
			}
		}
	} else {
		dnsDetail = "disabled"
	}
	fmt.Printf("│ %-24s │ %-6s │ %-18s │\n", "DNS Query ("+healthCfg.DNSRecordType+")", sym(res.DNSAlive), dnsDetail)
	fmt.Println("├──────────────────────────┴────────┴────────────────────┤")
	fmt.Printf("│ Score: %-3d / 100  (Raw: %d/%d)   State: %-14s │\n", res.Score, res.RawScore, res.MaxScore, state.String())
	fmt.Println("└────────────────────────────────────────────────────────┘\n")
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

	fmt.Println("Collecting local node & peer status...\n")

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
