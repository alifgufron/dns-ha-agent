package config

import (
	"strings"
	"testing"
	"time"
)

func validConfig() *Config {
	return &Config{
		Agent: AgentConfig{
			Interval:     5 * time.Second,
			Interface:    "vtnet0",
			VIPInterface: "vtnet1",
			VHID:         1,
		},
		CARP: CARPConfig{
			DemotionHealthy:   0,
			DemotionDegraded:  50,
			DemotionUnhealthy: 255,
		},
		Peer: PeerConfig{
			Enabled: true,
			Bind:    "10.0.0.1",
			Port:    ":8845",
			Token:   "secret",
			Peers:   []PeerEntry{{IP: "10.0.0.2", Name: "node-b"}},
		},
		Policy: PolicyConfig{Mode: "preempt"},
		Notify: NotifyConfig{Cooldown: 5 * time.Minute},
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"empty interval", func(c *Config) { c.Agent.Interval = 0 }, "agent.interval"},
		{"missing interface", func(c *Config) { c.Agent.Interface = "" }, "agent.interface"},
		{"missing vip_interface", func(c *Config) { c.Agent.VIPInterface = "" }, "agent.vip_interface"},
		{"bad vhid", func(c *Config) { c.Agent.VHID = 0 }, "agent.vhid"},
		{"bad bind_address", func(c *Config) { c.Health.BindAddress = "no-port" }, "health.bind_address"},
		{"weight over range", func(c *Config) { c.Health.Weights.DNS = 150 }, "health.weights.dns"},
		{"weight negative", func(c *Config) { c.Health.Weights.TCP = -1 }, "health.weights.tcp"},
		{"empty process name", func(c *Config) { c.Health.ProcessNames = []string{""} }, "process_names"},
		{"missing token", func(c *Config) { c.Peer.Token = "" }, "peer.token"},
		{"bad peer bind", func(c *Config) { c.Peer.Bind = "not-an-ip" }, "peer.bind"},
		{"bad peer port format", func(c *Config) { c.Peer.Port = "8845" }, "peer.port"},
		{"bad peer dns_port", func(c *Config) { c.Peer.DNSPort = "notaport" }, "peer.dns_port"},
		{"bad peer ip", func(c *Config) { c.Peer.Peers = []PeerEntry{{IP: "xyz", Name: "n"}} }, "peer.peers[0].ip"},
		{"tls missing cert", func(c *Config) { c.Peer.TLS = TLSServerConfig{Enabled: true, KeyFile: "k"} }, "peer.tls.cert_file"},
		{"tls missing key", func(c *Config) { c.Peer.TLS = TLSServerConfig{Enabled: true, CertFile: "c"} }, "peer.tls.key_file"},
		{"bad policy mode", func(c *Config) { c.Policy.Mode = "bogus" }, "policy.mode"},
		{"no cooldown", func(c *Config) { c.Notify.Cooldown = 0 }, "notify.cooldown"},
		{"negative confirm", func(c *Config) { c.Notify.Confirm = -1 }, "notify.confirm"},
		{"empty domain and domains", func(c *Config) { c.Health.DNSQuery = DNSQuery{Enabled: true} }, "health.dns_query.domain"},
		{"empty string in domains list", func(c *Config) { c.Health.DNSQuery = DNSQuery{Enabled: true, Domains: []string{""}} }, "health.dns_query.domains"},
		{"negative latency threshold", func(c *Config) { c.Health.DNSQuery = DNSQuery{Enabled: true, Domain: "google.com", LatencyThreshold: -1 * time.Second} }, "health.dns_query.latency_threshold"},
		{"invalid record type", func(c *Config) { c.Health.DNSQuery = DNSQuery{Enabled: true, Domain: "google.com", RecordType: "INVALID"} }, "health.dns_query.record_type"},
		{"slack missing url", func(c *Config) { c.Notify.Slack = SlackConfig{Enabled: true} }, "slack.webhook_url"},
		{"telegram missing token", func(c *Config) { c.Notify.Telegram = TelegramConfig{Enabled: true, ChatID: "1"} }, "telegram.bot_token"},
		{"telegram missing chat", func(c *Config) { c.Notify.Telegram = TelegramConfig{Enabled: true, BotToken: "t"} }, "telegram.chat_id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validConfig()
			tc.mutate(c)
			err := Validate(c)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestProcessListResolution(t *testing.T) {
	cases := []struct {
		name     string
		names    []string
		single   string
		expected string
	}{
		{"list wins", []string{"dnsdist", "named"}, "ignored", "dnsdist,named"},
		{"single fallback", nil, "named", "named"},
		{"default", nil, "", "dnsdist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := HealthConfig{ProcessNames: tc.names, ProcessName: tc.single}
			got := strings.Join(h.ProcessList(), ",")
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestTokenForPairwise(t *testing.T) {
	p := PeerConfig{Token: "global", Peers: []PeerEntry{
		{IP: "10.0.0.2", Name: "a", Token: "pair"},
		{IP: "10.0.0.3", Name: "b"},
	}}
	if got := p.TokenFor(p.Peers[0]); got != "pair" {
		t.Fatalf("expected pairwise token, got %q", got)
	}
	if got := p.TokenFor(p.Peers[1]); got != "global" {
		t.Fatalf("expected global fallback, got %q", got)
	}
}

// The vip_loss_alert rename must not break configs already deployed on nodes,
// so master_loss_alert stays readable and folds into the new field.
func TestDeprecatedMasterLossAlertAlias(t *testing.T) {
	old := true
	cfg := &Config{Notify: NotifyConfig{MasterLossAlert: &old}}
	applyDeprecatedAliases(cfg)
	if !cfg.Notify.VIPLossAlert {
		t.Error("master_loss_alert: true should set VIPLossAlert")
	}

	off := false
	cfg = &Config{Notify: NotifyConfig{MasterLossAlert: &off}}
	applyDeprecatedAliases(cfg)
	if cfg.Notify.VIPLossAlert {
		t.Error("master_loss_alert: false must stay false, not be silently re-enabled")
	}

	// New key set, legacy key absent — nothing to fold.
	cfg = &Config{Notify: NotifyConfig{VIPLossAlert: true}}
	applyDeprecatedAliases(cfg)
	if !cfg.Notify.VIPLossAlert {
		t.Error("vip_loss_alert should be preserved")
	}

	// Both present: the new key wins.
	cfg = &Config{Notify: NotifyConfig{VIPLossAlert: true, MasterLossAlert: &off}}
	applyDeprecatedAliases(cfg)
	if !cfg.Notify.VIPLossAlert {
		t.Error("vip_loss_alert should win over the legacy key")
	}
}

func TestPeerListenAddr(t *testing.T) {
	cases := []struct {
		name     string
		bind     string
		port     string
		expected string
	}{
		{"ipv4 with colon", "10.0.0.1", ":8845", "10.0.0.1:8845"},
		{"ipv4 without colon", "10.0.0.1", "8845", "10.0.0.1:8845"},
		{"ipv6 with colon", "fe80::1", ":8845", "[fe80::1]:8845"},
		{"ipv6 localhost", "::1", ":8845", "[::1]:8845"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := PeerConfig{Bind: tc.bind, Port: tc.port}
			got := p.ListenAddr()
			if got != tc.expected {
				t.Fatalf("ListenAddr() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestBindAddressListAndValidate(t *testing.T) {
	h := HealthConfig{
		BindAddresses: []string{"127.0.0.1:53", "[::1]:53"},
	}
	list := h.BindAddressList()
	if len(list) != 2 || list[0] != "127.0.0.1:53" || list[1] != "[::1]:53" {
		t.Fatalf("BindAddressList() failed, got %v", list)
	}

	cfg := &Config{
		Agent: AgentConfig{Interface: "vtnet0", VIPInterface: "vtnet1", VHID: 1},
		Health: HealthConfig{
			BindAddresses: []string{"127.0.0.1:53", "invalid-no-port", ""},
		},
		CARP: CARPConfig{DemotionHealthy: 0, DemotionDegraded: 50, DemotionUnhealthy: 255},
		Peer: PeerConfig{Enabled: false},
	}
	err := Validate(cfg)
	if err == nil {
		t.Fatal("expected error on invalid bind_addresses, got nil")
	}
}


