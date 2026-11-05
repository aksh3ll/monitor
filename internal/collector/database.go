package collector

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"admin/internal/config"
)

// DBServerInfo is shared by the MySQL and PostgreSQL collectors.
type DBServerInfo struct {
	Status        string     `json:"status"` // up | down
	Host          string     `json:"host"`
	Port          int        `json:"port"`                  // configured port
	ServerPort    int        `json:"server_port,omitempty"` // port reported by the server
	Version       string     `json:"version,omitempty"`
	UptimeSeconds int64      `json:"uptime_seconds,omitempty"`
	Connections   int        `json:"connections,omitempty"`
	Databases     []Database `json:"databases"`
	QueryError    string     `json:"query_error,omitempty"`
}

type Database struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"` // -1 when unknown
	Tables    int    `json:"tables,omitempty"`
	Owner     string `json:"owner,omitempty"`
}

// probe checks the server is accepting TCP connections.
func probe(ctx context.Context, host string, port int) bool {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func atoi64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NULL" {
		return -1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// ---------------------------------------------------------------- MySQL

type MySQL struct{ cfg config.Database }

func NewMySQL(cfg config.Database) *MySQL { return &MySQL{cfg: cfg} }

func (m *MySQL) Name() string { return "mysql" }

func (m *MySQL) Collect(ctx context.Context) (any, error) {
	info := DBServerInfo{Status: "down", Host: m.cfg.Host, Port: m.cfg.Port, Databases: []Database{}}
	if !probe(ctx, m.cfg.Host, m.cfg.Port) {
		return info, nil
	}
	info.Status = "up"
	if err := m.query(ctx, &info); err != nil {
		info.QueryError = err.Error()
	}
	return info, nil
}

func (m *MySQL) query(ctx context.Context, info *DBServerInfo) error {
	// Credentials go into a private temporary defaults file so they never
	// appear on the command line (visible in ps).
	f, err := os.CreateTemp("", "admin-mysql-*.cnf")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	fmt.Fprintf(f, "[client]\nuser=%q\npassword=%q\n", m.cfg.User, m.cfg.Password)
	if err := f.Close(); err != nil {
		return err
	}

	run := func(q string) ([][]string, error) {
		out, err := runCmd(ctx, nil, m.cfg.Binary,
			"--defaults-extra-file="+f.Name(),
			"--protocol=TCP", "-h", m.cfg.Host, "-P", strconv.Itoa(m.cfg.Port),
			"--batch", "--skip-column-names", "--connect-timeout=5",
			"-e", q)
		if err != nil {
			return nil, err
		}
		return parseTSV(out), nil
	}

	rows, err := run("SELECT VERSION(), @@port")
	if err != nil {
		return err
	}
	if len(rows) > 0 && len(rows[0]) >= 2 {
		info.Version = rows[0][0]
		info.ServerPort = atoi(rows[0][1])
	}

	if rows, err := run("SHOW GLOBAL STATUS WHERE Variable_name IN ('Uptime','Threads_connected')"); err == nil {
		for _, r := range rows {
			if len(r) < 2 {
				continue
			}
			switch r[0] {
			case "Uptime":
				info.UptimeSeconds = atoi64(r[1])
			case "Threads_connected":
				info.Connections = atoi(r[1])
			}
		}
	}

	rows, err = run(`SELECT s.SCHEMA_NAME, COALESCE(SUM(t.DATA_LENGTH + t.INDEX_LENGTH), 0), COUNT(t.TABLE_NAME)
		FROM information_schema.SCHEMATA s
		LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
		GROUP BY s.SCHEMA_NAME ORDER BY s.SCHEMA_NAME`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		info.Databases = append(info.Databases, Database{Name: r[0], SizeBytes: atoi64(r[1]), Tables: atoi(r[2])})
	}
	return nil
}

// ---------------------------------------------------------------- PostgreSQL

type Postgres struct{ cfg config.Database }

func NewPostgres(cfg config.Database) *Postgres { return &Postgres{cfg: cfg} }

func (p *Postgres) Name() string { return "postgres" }

func (p *Postgres) Collect(ctx context.Context) (any, error) {
	info := DBServerInfo{Status: "down", Host: p.cfg.Host, Port: p.cfg.Port, Databases: []Database{}}
	if !probe(ctx, p.cfg.Host, p.cfg.Port) {
		return info, nil
	}
	info.Status = "up"
	if err := p.query(ctx, &info); err != nil {
		info.QueryError = err.Error()
	}
	return info, nil
}

func (p *Postgres) query(ctx context.Context, info *DBServerInfo) error {
	// Password is passed through the environment, not the command line.
	env := append(os.Environ(), "PGPASSWORD="+p.cfg.Password, "PGCONNECT_TIMEOUT=5")
	dbname := p.cfg.Database
	if dbname == "" {
		dbname = "postgres"
	}
	run := func(q string) ([][]string, error) {
		out, err := runCmd(ctx, env, p.cfg.Binary,
			"-X", "-A", "-t", "-F", "\t", "-w",
			"-h", p.cfg.Host, "-p", strconv.Itoa(p.cfg.Port),
			"-U", p.cfg.User, "-d", dbname,
			"-c", q)
		if err != nil {
			return nil, err
		}
		return parseTSV(out), nil
	}

	rows, err := run(`SELECT current_setting('server_version'), current_setting('port'),
		EXTRACT(EPOCH FROM now() - pg_postmaster_start_time())::bigint,
		(SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend')`)
	if err != nil {
		return err
	}
	if len(rows) > 0 && len(rows[0]) >= 4 {
		info.Version = rows[0][0]
		info.ServerPort = atoi(rows[0][1])
		info.UptimeSeconds = atoi64(rows[0][2])
		info.Connections = atoi(rows[0][3])
	}

	rows, err = run(`SELECT d.datname,
		CASE WHEN has_database_privilege(d.datname, 'CONNECT') THEN pg_database_size(d.datname) END,
		pg_get_userbyid(d.datdba)
		FROM pg_database d WHERE NOT d.datistemplate ORDER BY d.datname`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if len(r) < 3 {
			continue
		}
		info.Databases = append(info.Databases, Database{Name: r[0], SizeBytes: atoi64(r[1]), Owner: r[2]})
	}
	return nil
}
