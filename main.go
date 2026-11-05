// Command admin monitors the services of the server (system, Docker, MySQL,
// PostgreSQL, iptables, systemd) and serves a web dashboard and REST API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"admin/internal/collector"
	"admin/internal/config"
	"admin/internal/server"
	"admin/internal/service"
	"admin/internal/store"
)

var version = "dev"

func usage() {
	fmt.Fprintf(os.Stderr, `admin %s - server services monitoring

Usage:
  admin run       [-config path] [-port n] [-listen addr]   run the server (default)
  admin install   [-config path] [-port n] [-listen addr]   install & start the systemd service
  admin uninstall [-config path] [-purge]                   stop & remove the systemd service
  admin version
`, version)
}

func main() {
	cmd, args := "run", os.Args[1:]
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	fl := flag.NewFlagSet(cmd, flag.ExitOnError)
	fl.Usage = usage
	configPath := fl.String("config", config.DefaultPath, "configuration file")
	port := fl.Int("port", 0, "listening port (overrides config)")
	listen := fl.String("listen", "", "listening address (overrides config)")
	purge := fl.Bool("purge", false, "uninstall: also remove the configuration")
	fl.Parse(args)

	var err error
	switch cmd {
	case "run":
		err = run(*configPath, *port, *listen)
	case "install":
		err = service.Install(service.InstallOptions{ConfigPath: *configPath, Port: *port, Listen: *listen})
	case "uninstall":
		err = service.Uninstall(*configPath, *purge)
	case "version":
		fmt.Println(version)
	case "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func run(configPath string, port int, listen string) error {
	cfg, exists, err := config.Load(configPath)
	if err != nil {
		return err
	}
	if !exists {
		log.Printf("config %s not found, using defaults", configPath)
	}
	if port != 0 {
		cfg.Port = port
	}
	if listen != "" {
		cfg.Listen = listen
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.Auth.Username == "" {
		log.Printf("WARNING: authentication disabled (auth.username is empty)")
	}

	st := store.New()
	var jobs []collector.Job
	add := func(c collector.Collector, enabled bool, interval int) {
		st.Register(c.Name(), enabled, interval)
		if enabled {
			jobs = append(jobs, collector.Job{Collector: c, Interval: time.Duration(interval) * time.Second})
		}
	}
	add(collector.NewSystem(cfg.System.Mounts), cfg.System.Enabled, cfg.System.Interval)
	add(collector.NewDocker(cfg.Docker.Socket), cfg.Docker.Enabled, cfg.Docker.Interval)
	add(collector.NewMySQL(cfg.MySQL), cfg.MySQL.Enabled, cfg.MySQL.Interval)
	add(collector.NewPostgres(cfg.Postgres), cfg.Postgres.Enabled, cfg.Postgres.Interval)
	add(collector.NewIptables(cfg.Iptables), cfg.Iptables.Enabled, cfg.Iptables.Interval)
	add(collector.NewSystemd(cfg.Systemd), cfg.Systemd.Enabled, cfg.Systemd.Interval)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	collectorsDone := make(chan struct{})
	go func() {
		collector.Run(ctx, st, jobs)
		close(collectorsDone)
	}()

	addr := net.JoinHostPort(cfg.Listen, strconv.Itoa(cfg.Port))
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(cfg, st).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Printf("admin %s listening on http://%s", version, addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		stop()
		<-collectorsDone
		return err
	case <-ctx.Done():
	}
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdownCtx)
	<-collectorsDone
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
