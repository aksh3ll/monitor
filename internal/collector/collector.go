// Package collector gathers service information in background goroutines
// and publishes it to the store.
package collector

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"admin/internal/store"
)

type Collector interface {
	Name() string
	Collect(ctx context.Context) (any, error)
}

type Job struct {
	Collector Collector
	Interval  time.Duration
}

// Run starts one goroutine per job. Each collects immediately, then on every
// tick, until ctx is cancelled. Run blocks until all goroutines have exited.
func Run(ctx context.Context, st *store.Store, jobs []Job) {
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			ticker := time.NewTicker(j.Interval)
			defer ticker.Stop()
			for {
				collectOnce(ctx, st, j)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}(j)
	}
	wg.Wait()
}

func collectOnce(ctx context.Context, st *store.Store, j Job) {
	timeout := min(j.Interval, 30*time.Second)
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	data, err := safeCollect(cctx, j.Collector)
	if err != nil && ctx.Err() == nil {
		log.Printf("collector %s: %v", j.Collector.Name(), err)
	}
	st.Update(j.Collector.Name(), data, err, time.Since(start))
}

func safeCollect(ctx context.Context, c Collector) (data any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c.Collect(ctx)
}

// runCmd executes a command and returns its stdout. Stderr is included in the error.
func runCmd(ctx context.Context, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := cleanStderr(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}

// cleanStderr drops client warnings so only the real error remains.
func cleanStderr(s string) string {
	var keep []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.Contains(line, "WARNING:") || strings.Contains(line, "[Warning]") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, " ")
}

// parseTSV splits tab-separated client output into rows.
func parseTSV(out string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows
}
