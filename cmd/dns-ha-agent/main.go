package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/alifgufron/dns-ha-agent/internal/agent"
	"github.com/alifgufron/dns-ha-agent/internal/config"
	"github.com/alifgufron/dns-ha-agent/internal/logger"
)

func logToSyslog(tag, msg string) {
	exec.Command("logger", "-t", tag, msg).Run()
}

func main() {
	configPath := flag.String("config", "/usr/local/etc/dns-ha-agent.yaml", "path to config file")
	checkOnly := flag.Bool("t", false, "test config and exit")
	flag.Parse()

	if *checkOnly {
		errs := config.CheckConfig(*configPath)
		if len(errs) > 0 {
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Fprintf(os.Stderr, "  ╔══════════════════════════════════════════════════╗\n")
			fmt.Fprintf(os.Stderr, "  ║       DNS-HA-AGENT — CONFIG ERROR          ║\n")
			fmt.Fprintf(os.Stderr, "  ╚══════════════════════════════════════════════════╝\n")
			fmt.Fprintf(os.Stderr, "\n")
			for _, e := range errs {
				fmt.Fprintf(os.Stderr, "  • %s\n", e)
				logToSyslog("dns-ha-agent", "config error: "+e)
			}
			fmt.Fprintf(os.Stderr, "\n")
			fmt.Fprintf(os.Stderr, "  Fix the config and try again.\n")
			fmt.Fprintf(os.Stderr, "\n")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "  Config OK: %s\n", *configPath)
		os.Exit(0)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  ╔══════════════════════════════════════════════════╗\n")
		fmt.Fprintf(os.Stderr, "  ║       DNS-HA-AGENT — CONFIG ERROR          ║\n")
		fmt.Fprintf(os.Stderr, "  ╚══════════════════════════════════════════════════╝\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  File: %s\n", *configPath)
		fmt.Fprintf(os.Stderr, "  %s\n", err)
		logToSyslog("dns-ha-agent", fmt.Sprintf("config error: %s", err))
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  Fix the config and try again.\n")
		fmt.Fprintf(os.Stderr, "\n")
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFile)
	log.Info("[AGENT] starting dns-ha-agent", "config", *configPath)

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
