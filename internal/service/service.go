// Package service installs and removes the systemd unit.
package service

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"admin/internal/config"
)

const (
	Name       = "admin"
	BinaryPath = "/usr/local/bin/admin"
	UnitPath   = "/etc/systemd/system/admin.service"
)

const unitTemplate = `[Unit]
Description=Admin - server services monitoring
After=network-online.target docker.service mysql.service postgresql.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run -config %s
Restart=on-failure
RestartSec=5
# Root is required to read iptables rules and the Docker socket.
User=root
NoNewPrivileges=true
PrivateTmp=true
ProtectHome=read-only

[Install]
WantedBy=multi-user.target
`

type InstallOptions struct {
	ConfigPath string
	Port       int    // 0 keeps the configured / default port
	Listen     string // "" keeps the configured / default address
}

func Install(opts InstallOptions) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("install must be run as root")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found: only systemd is supported")
	}

	if err := copySelf(BinaryPath); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Println("binary installed:", BinaryPath)

	cfg, exists, err := config.Load(opts.ConfigPath)
	if err != nil {
		return err
	}
	if !exists {
		cfg.Auth.Username = "admin"
		cfg.Auth.Password = randomPassword()
		fmt.Printf("generated credentials: admin / %s\n", cfg.Auth.Password)
	}
	if opts.Port != 0 {
		cfg.Port = opts.Port
	}
	if opts.Listen != "" {
		cfg.Listen = opts.Listen
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.Save(opts.ConfigPath); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	fmt.Println("config written:", opts.ConfigPath)

	unit := fmt.Sprintf(unitTemplate, BinaryPath, opts.ConfigPath)
	if err := os.WriteFile(UnitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	fmt.Println("unit written:", UnitPath)

	for _, args := range [][]string{
		{"daemon-reload"},
		{"enable", Name},
		{"restart", Name},
	} {
		if err := systemctl(args...); err != nil {
			return err
		}
	}
	fmt.Printf("service %s started, listening on %s:%d\n", Name, cfg.Listen, cfg.Port)
	return nil
}

func Uninstall(configPath string, purge bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall must be run as root")
	}
	_ = systemctl("disable", "--now", Name)
	for _, p := range []string{UnitPath, BinaryPath} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_ = systemctl("daemon-reload")
	if purge {
		if err := os.RemoveAll(filepath.Dir(configPath)); err != nil {
			return err
		}
		fmt.Println("config removed:", filepath.Dir(configPath))
	}
	fmt.Println("service removed")
	return nil
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %v: %w", args, err)
	}
	return nil
}

func copySelf(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if src, err = filepath.EvalSymlinks(src); err != nil {
		return err
	}
	if src == dst {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Write to a temp file then rename, so a running binary can be replaced.
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func randomPassword() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
