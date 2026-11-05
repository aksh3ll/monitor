package collector

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"admin/internal/config"
)

type SystemdInfo struct {
	Total    int           `json:"total"`
	Running  int           `json:"running"`
	Failed   int           `json:"failed"`
	Inactive int           `json:"inactive"`
	Missing  int           `json:"missing"` // configured but not installed
	Units    []SystemdUnit `json:"units"`
}

type SystemdUnit struct {
	Name        string `json:"name"`
	Load        string `json:"load"`   // loaded, not-found, masked…
	Active      string `json:"active"` // active, inactive, failed, activating…
	Sub         string `json:"sub"`    // running, exited, dead, failed…
	Description string `json:"description"`
}

type Systemd struct{ cfg config.Systemd }

func NewSystemd(cfg config.Systemd) *Systemd { return &Systemd{cfg: cfg} }

func (s *Systemd) Name() string { return "systemd" }

func (s *Systemd) Collect(ctx context.Context) (any, error) {
	if _, err := exec.LookPath(s.cfg.Binary); err != nil {
		return nil, fmt.Errorf("%s not found", s.cfg.Binary)
	}
	patterns := unitPatterns(s.cfg.Units)
	args := []string{"list-units", "--type=service", "--no-legend", "--no-pager", "--plain"}
	// Without --all, systemctl only lists active and failed units. When units are
	// named explicitly, stopped ones must show up too.
	if s.cfg.All || len(patterns) > 0 {
		args = append(args, "--all")
	}
	args = append(args, patterns...)
	out, err := runCmd(ctx, nil, s.cfg.Binary, args...)
	if err != nil {
		return nil, err
	}
	units := parseListUnits(out)
	units = addMissingUnits(units, patterns)

	info := SystemdInfo{Units: units, Total: len(units)}
	for _, u := range units {
		switch {
		case u.Load == "not-found":
			info.Missing++
		case u.Active == "failed":
			info.Failed++
		case u.Active == "active" && u.Sub == "running":
			info.Running++
		case u.Active == "inactive":
			info.Inactive++
		}
	}
	return info, nil
}

// unitPatterns appends ".service" to names given without a suffix, since
// systemctl does not match "cron" against "cron.service".
func unitPatterns(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !strings.Contains(n, ".") {
			n += ".service"
		}
		out = append(out, n)
	}
	return out
}

// parseListUnits parses `systemctl list-units --plain --no-legend` output:
// UNIT LOAD ACTIVE SUB DESCRIPTION...
func parseListUnits(out string) []SystemdUnit {
	units := []SystemdUnit{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		// Some versions prefix failed / not-found units with a status marker.
		if len(f) > 0 && (f[0] == "●" || f[0] == "*") {
			f = f[1:]
		}
		if len(f) < 4 {
			continue
		}
		units = append(units, SystemdUnit{
			Name: f[0], Load: f[1], Active: f[2], Sub: f[3],
			Description: strings.Join(f[4:], " "),
		})
	}
	return units
}

// addMissingUnits reports explicitly configured units (no glob) that systemctl
// did not return, so a typo or an uninstalled service is visible.
func addMissingUnits(units []SystemdUnit, patterns []string) []SystemdUnit {
	seen := make(map[string]bool, len(units))
	for _, u := range units {
		seen[u.Name] = true
	}
	for _, p := range patterns {
		if strings.ContainsAny(p, "*?[") || seen[p] {
			continue
		}
		seen[p] = true
		units = append(units, SystemdUnit{Name: p, Load: "not-found", Active: "inactive", Sub: "dead"})
	}
	return units
}
