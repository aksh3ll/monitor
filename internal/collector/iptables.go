package collector

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"admin/internal/config"
)

type IptablesInfo struct {
	Families  []string       `json:"families"`
	Policies  []ChainPolicy  `json:"policies"`
	OpenPorts []FirewallPort `json:"open_ports"`
	Rules     []FirewallRule `json:"rules"`
}

type ChainPolicy struct {
	Family string `json:"family"`
	Chain  string `json:"chain"`
	Policy string `json:"policy"`
}

// FirewallPort is a rule that matches a destination port.
type FirewallPort struct {
	Family   string `json:"family"`
	Chain    string `json:"chain"`
	Ports    string `json:"ports"`
	Protocol string `json:"protocol"`
	Target   string `json:"target"`
	Source   string `json:"source,omitempty"`
	Dest     string `json:"dest,omitempty"`
	InIface  string `json:"in_iface,omitempty"`
}

type FirewallRule struct {
	Family string `json:"family"`
	Chain  string `json:"chain"`
	Rule   string `json:"rule"`
}

type Iptables struct{ cfg config.Iptables }

func NewIptables(cfg config.Iptables) *Iptables { return &Iptables{cfg: cfg} }

func (t *Iptables) Name() string { return "iptables" }

func (t *Iptables) Collect(ctx context.Context) (any, error) {
	info := IptablesInfo{Policies: []ChainPolicy{}, OpenPorts: []FirewallPort{}, Rules: []FirewallRule{}}
	type src struct{ family, bin string }
	sources := []src{{"ipv4", t.cfg.Binary}}
	if t.cfg.IPv6 {
		sources = append(sources, src{"ipv6", t.cfg.Binary6})
	}
	var errs []error
	for _, s := range sources {
		if _, err := exec.LookPath(s.bin); err != nil {
			errs = append(errs, fmt.Errorf("%s not found", s.bin))
			continue
		}
		out, err := runCmd(ctx, nil, s.bin, "-w", "5", "-S")
		if err != nil {
			errs = append(errs, err)
			continue
		}
		info.Families = append(info.Families, s.family)
		parseIptablesS(s.family, out, &info)
	}
	if len(info.Families) == 0 {
		return nil, errors.Join(errs...)
	}
	return info, errors.Join(errs...)
}

// parseIptablesS parses the output of `iptables -S`.
func parseIptablesS(family, out string, info *IptablesInfo) {
	for _, line := range strings.Split(out, "\n") {
		tok := tokenize(line)
		if len(tok) < 2 {
			continue
		}
		switch tok[0] {
		case "-P":
			if len(tok) >= 3 {
				info.Policies = append(info.Policies, ChainPolicy{Family: family, Chain: tok[1], Policy: tok[2]})
			}
		case "-A":
			chain := tok[1]
			info.Rules = append(info.Rules, FirewallRule{Family: family, Chain: chain, Rule: strings.Join(tok[2:], " ")})
			p := FirewallPort{Family: family, Chain: chain, Protocol: "all"}
			neg := ""
			for i := 2; i < len(tok); i++ {
				if tok[i] == "!" {
					neg = "!"
					continue
				}
				val := ""
				if i+1 < len(tok) {
					val = neg + tok[i+1]
				}
				switch tok[i] {
				case "-p":
					p.Protocol = val
				case "--dport", "--dports", "--destination-port", "--destination-ports":
					p.Ports = strings.ReplaceAll(val, ":", "-")
				case "-s":
					p.Source = val
				case "-d":
					p.Dest = val
				case "-i":
					p.InIface = val
				case "-j", "-g":
					p.Target = val
				default:
					neg = ""
					continue
				}
				neg = ""
				i++
			}
			if p.Ports != "" {
				info.OpenPorts = append(info.OpenPorts, p)
			}
		}
	}
}

// tokenize splits a rule line on spaces, keeping double-quoted strings together.
func tokenize(line string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote, has := false, false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && inQuote && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case c == '"':
			inQuote = !inQuote
			has = true
		case c == ' ' && !inQuote:
			if has {
				tokens = append(tokens, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteByte(c)
			has = true
		}
	}
	if has {
		tokens = append(tokens, cur.String())
	}
	return tokens
}
