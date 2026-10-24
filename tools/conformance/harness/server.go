package harness

import (
	"bufio"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// DB describes the throwaway PostgreSQL database a server under test uses.
type DB struct {
	Host string
	Port int
	Name string
	User string
}

// Password returns PGPASSWORD if set, otherwise the matching pgpass entry. The
// value is never logged.
func (d DB) Password() (string, error) {
	if v := os.Getenv("PGPASSWORD"); v != "" {
		return v, nil
	}
	path := os.Getenv("PGPASSFILE")
	if path == "" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			path = filepath.Join(appData, "postgresql", "pgpass.conf")
		} else {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, ".pgpass")
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("no PGPASSWORD and no pgpass file: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	match := func(pattern, value string) bool { return pattern == "*" || pattern == value }
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 5)
		if len(parts) != 5 {
			continue
		}
		if match(parts[0], d.Host) && match(parts[1], fmt.Sprint(d.Port)) && match(parts[2], d.Name) && match(parts[3], d.User) {
			return parts[4], nil
		}
	}
	return "", fmt.Errorf("no pgpass entry for %s:%d/%s user %s", d.Host, d.Port, d.Name, d.User)
}

// Reset drops and recreates the public schema so the server starts from an
// empty database and every id it allocates is reproducible.
func (d DB) Reset() error {
	pw, err := d.Password()
	if err != nil {
		return err
	}
	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable", d.Host, d.Port, d.Name, d.User, pw)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public;`)
	return err
}

// ServerConfig says how to start the server under test.
type ServerConfig struct {
	Bin     string
	Port    int
	DB      DB
	Secret  string
	HostURL string
	LogPath string
	Env     []string
}

// Server is a running server process.
type Server struct {
	cmd  *exec.Cmd
	log  *os.File
	Port int
}

// HTTPBase is the server's HTTP origin.
func (s *Server) HTTPBase() string { return fmt.Sprintf("http://127.0.0.1:%d", s.Port) }

// WSURL is the IM WebSocket endpoint.
func (s *Server) WSURL() string { return fmt.Sprintf("ws://127.0.0.1:%d/ws/im", s.Port) }

// StartServer launches the binary and waits until /api/health answers.
func StartServer(cfg ServerConfig) (*Server, error) {
	pw, err := cfg.DB.Password()
	if err != nil {
		return nil, err
	}
	logf, err := os.Create(cfg.LogPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(cfg.Bin)
	cmd.Env = append(os.Environ(),
		"DB_HOST="+cfg.DB.Host,
		fmt.Sprintf("DB_PORT=%d", cfg.DB.Port),
		"DB_NAME="+cfg.DB.Name,
		"DB_USER="+cfg.DB.User,
		"DB_PASSWORD="+pw,
		"DB_SSLMODE=disable",
		"JWT_SECRET="+cfg.Secret,
		fmt.Sprintf("IM_PORT=%d", cfg.Port),
		"MAIN_API_BASE_URL="+cfg.HostURL,
	)
	cmd.Env = append(cmd.Env, cfg.Env...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		return nil, err
	}
	s := &Server{cmd: cmd, log: logf, Port: cfg.Port}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			logf.Close()
			return nil, fmt.Errorf("server exited during startup: %v (see %s)", err, cfg.LogPath)
		default:
		}
		resp, err := http.Get(s.HTTPBase() + "/api/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				go func() { <-exited }()
				return s, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	s.Stop()
	return nil, fmt.Errorf("server not healthy within 20s (see %s)", cfg.LogPath)
}

// Stop kills the process and closes its log.
func (s *Server) Stop() {
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	time.Sleep(200 * time.Millisecond)
	s.log.Close()
}
