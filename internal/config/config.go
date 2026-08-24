package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Agent    AgentConfig  `yaml:"agent"`
	Health   HealthConfig `yaml:"health"`
	CARP     CARPConfig   `yaml:"carp"`
	Peer     PeerConfig   `yaml:"peer"`
	Policy   PolicyConfig `yaml:"policy"`
	Notify   NotifyConfig `yaml:"notify"`
	LogFile  string       `yaml:"log_file"`
	LogLevel string       `yaml:"log_level"`
}

type AgentConfig struct {
	Interval     time.Duration `yaml:"interval"`
	Interface    string        `yaml:"interface"`
	VIPInterface string        `yaml:"vip_interface"`
	VHID         int           `yaml:"vhid"`
	StateFile    string        `yaml:"state_file"`
	// RecoveryConfirm is how many consecutive fully-healthy intervals a node
	// whose VIP interface is down must pass before it reclaims the VIP.
	// Zero means "unset" and falls back to DefaultRecoveryConfirm.
	RecoveryConfirm int `yaml:"recovery_confirm"`
}

type HealthConfig struct {
	ProcessCheck bool          `yaml:"process_check"`
	ProcessName  string        `yaml:"process_name"`
	ProcessNames []string      `yaml:"process_names"`
	TCPCheck     bool          `yaml:"tcp_check"`
	UDPCheck     bool          `yaml:"udp_check"`
	DNSQuery     DNSQuery      `yaml:"dns_query"`
	BindAddress  string        `yaml:"bind_address"`
	Weights      WeightsConfig `yaml:"weights"`
}

type WeightsConfig struct {
	Process int `yaml:"process"`
	TCP     int `yaml:"tcp"`
	UDP     int `yaml:"udp"`
	DNS     int `yaml:"dns"`
}

// ProcessList returns the process names to check, honoring process_names
// (list), falling back to process_name (single), then the default.
func (h HealthConfig) ProcessList() []string {
	if len(h.ProcessNames) > 0 {
		return h.ProcessNames
	}
	if h.ProcessName != "" {
		return []string{h.ProcessName}
	}
	return []string{"dnsdist"}
}

type DNSQuery struct {
	Enabled bool          `yaml:"enabled"`
	Domain  string        `yaml:"domain"`
	Timeout time.Duration `yaml:"timeout"`
}

type CARPConfig struct {
	DemotionHealthy   int `yaml:"demotion_healthy"`
	DemotionDegraded  int `yaml:"demotion_degraded"`
	DemotionUnhealthy int `yaml:"demotion_unhealthy"`
}

type PeerConfig struct {
	Enabled bool            `yaml:"enabled"`
	Bind    string          `yaml:"bind"`
	Port    string          `yaml:"port"`
	Token   string          `yaml:"token"`
	Ping    bool            `yaml:"ping"`
	TLS     TLSServerConfig `yaml:"tls"`
	Peers   []PeerEntry     `yaml:"peers"`
	// DNSPort is the peer's DNS port, probed only when a heartbeat fails, to
	// tell "agent is down" apart from "the peer stopped serving DNS".
	// Empty means 53.
	DNSPort string `yaml:"dns_port"`
}

type TLSServerConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// ListenAddr returns bind IP + port (e.g. "10.0.0.1:8080" or "[::1]:8080") for HTTP server
func (p PeerConfig) ListenAddr() string {
	return net.JoinHostPort(p.Bind, p.PortNum())
}

// PortNum returns just the port number (e.g. "8080") for peer client URLs
func (p PeerConfig) PortNum() string {
	return strings.TrimPrefix(p.Port, ":")
}

// TokenFor returns the pairwise token for a peer, or the global token.
func (p PeerConfig) TokenFor(peer PeerEntry) string {
	if peer.Token != "" {
		return peer.Token
	}
	return p.Token
}

type PeerEntry struct {
	IP    string `yaml:"ip"`
	Name  string `yaml:"name"`
	Token string `yaml:"token"`
}

type PolicyConfig struct {
	Mode string `yaml:"mode"`
}

type NotifyConfig struct {
	Email    EmailConfig    `yaml:"email"`
	Slack    SlackConfig    `yaml:"slack"`
	Telegram TelegramConfig `yaml:"telegram"`
	Cooldown time.Duration  `yaml:"cooldown"`
	// Confirm is how many consecutive cycles a state must hold before a
	// state-change notification is sent. It debounces transient dips without
	// delaying the actual failover, which is driven by the CARP decision.
	// Zero means "unset" and falls back to DefaultNotifyConfirm.
	Confirm int `yaml:"confirm"`
	// VIPLossAlert warns when this node loses the VIP with no peer entitled to
	// take it. Role-neutral by design: every node runs the same config and
	// watches only its own VIP, whichever role it happens to hold.
	VIPLossAlert bool `yaml:"vip_loss_alert"`
	// MasterLossAlert is the pre-rename name of VIPLossAlert. Kept so an
	// already-installed config keeps working; folded into VIPLossAlert on load.
	MasterLossAlert *bool `yaml:"master_loss_alert"`
}

type SlackConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"`
}

type TelegramConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

type EmailConfig struct {
	Enabled  bool     `yaml:"enabled"`
	SMTPHost string   `yaml:"smtp_host"`
	SMTPPort int      `yaml:"smtp_port"`
	Username string   `yaml:"username"`
	Password string   `yaml:"password"`
	From     string   `yaml:"from"`
	To       []string `yaml:"to"`
}

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

var knownKeys = map[string]map[string]bool{
	"agent":     {"interval": true, "interface": true, "vip_interface": true, "vhid": true, "state_file": true, "recovery_confirm": true},
	"log_file":  {},
	"log_level": {},
	"health":    {"process_check": true, "process_name": true, "process_names": true, "tcp_check": true, "udp_check": true, "dns_query": true, "bind_address": true, "weights": true},
	"carp":      {"demotion_healthy": true, "demotion_degraded": true, "demotion_unhealthy": true},
	"peer":      {"enabled": true, "bind": true, "port": true, "token": true, "ping": true, "tls": true, "peers": true, "dns_port": true},
	"policy":    {"mode": true},
	// master_loss_alert is the pre-rename name of vip_loss_alert, still accepted
	// so an already-installed config keeps working after an upgrade.
	"notify": {"email": true, "slack": true, "telegram": true, "cooldown": true, "confirm": true, "vip_loss_alert": true, "master_loss_alert": true},
}

var nestedKeys = map[string]map[string]bool{
	"dns_query":  {"enabled": true, "domain": true, "timeout": true},
	"email":      {"enabled": true, "smtp_host": true, "smtp_port": true, "username": true, "password": true, "from": true, "to": true},
	"weights":    {"process": true, "tcp": true, "udp": true, "dns": true},
	"tls":        {"enabled": true, "cert_file": true, "key_file": true},
	"slack":      {"enabled": true, "webhook_url": true},
	"telegram":   {"enabled": true, "bot_token": true, "chat_id": true},
	"peer.peers": {"ip": true, "name": true, "token": true},
}

func CheckConfig(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read config: %v", err)}
	}

	var rawNode yaml.Node
	if err := yaml.Unmarshal(data, &rawNode); err != nil {
		return []string{fmt.Sprintf("YAML syntax error: %v", err)}
	}

	var errs []string
	errs = append(errs, checkUnknownKeys(&rawNode, "")...)

	// Always try to parse + validate to collect ALL errors
	data = expandEnv(data)

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		errs = append(errs, fmt.Sprintf("parse config: %v", err))
		return errs
	}
	applyDeprecatedAliases(&cfg)

	if err := Validate(&cfg); err != nil {
		for _, e := range strings.Split(err.Error(), "\n") {
			if e != "" {
				errs = append(errs, e)
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var rawNode yaml.Node
	if err := yaml.Unmarshal(data, &rawNode); err != nil {
		return nil, fmt.Errorf("YAML syntax error: %v", err)
	}

	errs := checkUnknownKeys(&rawNode, "")
	if len(errs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errs, "\n"))
	}

	data = expandEnv(data)

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDeprecatedAliases(&cfg)

	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("%s", err.Error())
	}

	return &cfg, nil
}

// applyDeprecatedAliases folds renamed keys into their current field so an
// already-installed config keeps working after an upgrade. The new key wins if
// both are present.
func applyDeprecatedAliases(cfg *Config) {
	if !cfg.Notify.VIPLossAlert && cfg.Notify.MasterLossAlert != nil {
		cfg.Notify.VIPLossAlert = *cfg.Notify.MasterLossAlert
	}
}

func checkUnknownKeys(node *yaml.Node, prefix string) []string {
	var errs []string
	// DocumentNode wraps the root MappingNode
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return checkUnknownKeys(node.Content[0], prefix)
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]
		key := keyNode.Value

		switch prefix {
		case "":
			if _, ok := knownKeys[key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown top-level key %q — valid: agent, health, carp, peer, policy, notify, log_file, log_level", keyNode.Line, key))
			} else if valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkUnknownKeys(valNode, key)...)
			}

		case "agent":
			if _, ok := knownKeys["agent"][key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under 'agent:' — valid: interval, interface, vip_interface, vhid, state_file, recovery_confirm", keyNode.Line, key))
			}

		case "health":
			if _, ok := knownKeys["health"][key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under 'health:' — valid: process_check, process_name, process_names, tcp_check, udp_check, dns_query, bind_address, weights", keyNode.Line, key))
			} else if key == "dns_query" && valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkMappingKeys(valNode, "dns_query")...)
			} else if key == "weights" && valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkMappingKeys(valNode, "weights")...)
			}

		case "carp":
			if _, ok := knownKeys["carp"][key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under 'carp:' — valid: demotion_healthy, demotion_degraded, demotion_unhealthy", keyNode.Line, key))
			}

		case "peer":
			if _, ok := knownKeys["peer"][key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under 'peer:' — valid: enabled, bind, port, token, ping, tls, peers, dns_port", keyNode.Line, key))
			} else if key == "peers" && valNode.Kind == yaml.SequenceNode {
				errs = append(errs, checkPeerEntries(valNode)...)
			} else if key == "tls" && valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkMappingKeys(valNode, "tls")...)
			}

		case "policy":
			if _, ok := knownKeys["policy"][key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under 'policy:' — valid: mode", keyNode.Line, key))
			}

		case "notify":
			if _, ok := knownKeys["notify"][key]; !ok {
				errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under 'notify:' — valid: email, slack, telegram, cooldown, confirm, vip_loss_alert", keyNode.Line, key))
			} else if key == "email" && valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkMappingKeys(valNode, "email")...)
			} else if key == "slack" && valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkMappingKeys(valNode, "slack")...)
			} else if key == "telegram" && valNode.Kind == yaml.MappingNode {
				errs = append(errs, checkMappingKeys(valNode, "telegram")...)
			}
		}
	}

	return errs
}

func checkMappingKeys(node *yaml.Node, context string) []string {
	var errs []string
	if node.Kind != yaml.MappingNode {
		return nil
	}

	valid, ok := nestedKeys[context]
	if !ok {
		return nil
	}

	for i := 0; i < len(node.Content)-1; i += 2 {
		key := node.Content[i].Value
		if !valid[key] {
			var validList []string
			for k := range valid {
				validList = append(validList, k)
			}
			errs = append(errs, fmt.Sprintf("  line %d: unknown key %q under '%s:' — valid: %s",
				node.Content[i].Line, key, context, strings.Join(validList, ", ")))
		}
	}
	return errs
}

func checkPeerEntries(node *yaml.Node) []string {
	var errs []string
	if node.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range node.Content {
		if item.Kind == yaml.MappingNode {
			errs = append(errs, checkMappingKeys(item, "peer.peers")...)
		}
	}
	return errs
}

func expandEnv(data []byte) []byte {
	return envPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		envVar := string(match[2 : len(match)-1])
		val := os.Getenv(envVar)
		if val == "" {
			return match
		}
		return []byte(val)
	})
}
