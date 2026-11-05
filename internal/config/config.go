// Package config loads and saves the application configuration (JSON file).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultPath = "/etc/admin/config.json"

type Config struct {
	Listen string `json:"listen"`
	Port   int    `json:"port"`
	Auth   Auth   `json:"auth"`

	System   System   `json:"system"`
	Docker   Docker   `json:"docker"`
	MySQL    Database `json:"mysql"`
	Postgres Database `json:"postgres"`
	Iptables Iptables `json:"iptables"`
	Systemd  Systemd  `json:"systemd"`
}

// Auth enables HTTP Basic authentication when Username is not empty.
type Auth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type System struct {
	Enabled  bool `json:"enabled"`
	Interval int  `json:"interval_seconds"`
	// Mounts restricts disk usage to these mount points; empty means auto-detect.
	Mounts []string `json:"mounts"`
}

type Docker struct {
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval_seconds"`
	Socket   string `json:"socket"`
}

type Database struct {
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval_seconds"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	// Database is the database to connect to (PostgreSQL only).
	Database string `json:"database,omitempty"`
	// Binary is the client used for queries (mysql / psql).
	Binary string `json:"binary"`
}

type Iptables struct {
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval_seconds"`
	Binary   string `json:"binary"`
	IPv6     bool   `json:"ipv6"`
	Binary6  string `json:"binary6"`
}

type Systemd struct {
	Enabled  bool   `json:"enabled"`
	Interval int    `json:"interval_seconds"`
	Binary   string `json:"binary"`
	// Units restricts the list to these services (names or globs, ".service"
	// optional); empty lists every service systemd has loaded.
	Units []string `json:"units"`
	// All also lists inactive services when Units is empty.
	All bool `json:"all"`
}

func Default() *Config {
	return &Config{
		Listen: "0.0.0.0",
		Port:   8090,
		System: System{Enabled: true, Interval: 5},
		Docker: Docker{Enabled: true, Interval: 10, Socket: "/var/run/docker.sock"},
		MySQL: Database{
			Enabled: true, Interval: 30, Host: "127.0.0.1", Port: 3306,
			User: "root", Binary: "mysql",
		},
		Postgres: Database{
			Enabled: true, Interval: 30, Host: "127.0.0.1", Port: 5432,
			User: "postgres", Database: "postgres", Binary: "psql",
		},
		Iptables: Iptables{
			Enabled: true, Interval: 30, Binary: "iptables",
			IPv6: false, Binary6: "ip6tables",
		},
		Systemd: Systemd{Enabled: true, Interval: 15, Binary: "systemctl"},
	}
}

// Load reads the config at path on top of the defaults.
// A missing file is not an error: defaults are returned with exists=false.
func Load(path string) (cfg *Config, exists bool, err error) {
	cfg = Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, cfg.Validate()
}

func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid port %d", c.Port)
	}
	for _, iv := range []*int{&c.System.Interval, &c.Docker.Interval, &c.MySQL.Interval, &c.Postgres.Interval, &c.Iptables.Interval, &c.Systemd.Interval} {
		if *iv < 1 {
			*iv = 10
		}
	}
	return nil
}

// Save writes the config with 0600 permissions since it holds credentials.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
