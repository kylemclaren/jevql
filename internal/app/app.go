// Package app is the jevpsql command: flags, connection, -c / -f / REPL.
package app

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/term"

	"github.com/mclaren/jevpsql/internal/cache"
	"github.com/mclaren/jevpsql/internal/exec"
	"github.com/mclaren/jevpsql/internal/parse"
	"github.com/mclaren/jevpsql/internal/psqlout"
	"github.com/mclaren/jevpsql/internal/typesafe"
)

const (
	ExitOK        = 0
	ExitSQL       = 1
	ExitAPI       = 2
	defaultAPIURL = "https://api.typesafe.ai/v1/systemone"
	version       = "0.1.0"
)

// Config is the parsed command line.
type Config struct {
	Command     string
	File        string
	Host        string
	Port        string
	User        string
	DBName      string
	NoPassword  bool
	ForcePass   bool
	APIKey      string
	APIURL      string
	Model       string
	Threshold   float64
	BatchSize   int
	Concurrency int
	MaxRows     int
	MaxChars    int
	CachePath   string
	NoCache     bool
	Explain     bool
	Timing      bool
	CSV         bool
	JSON        bool
	Expanded    bool
	Verbose     bool
	Columns     string
	ShowVersion bool
	Positional  []string
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseFlags(args []string, stderr io.Writer) (*Config, error) {
	c := &Config{}
	fs := flag.NewFlagSet("jevpsql", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.Command, "c", "", "run one statement (or several separated by ;) and exit")
	fs.StringVar(&c.File, "f", "", "run statements from file and exit")
	fs.StringVar(&c.Host, "h", "", "database server host (default $PGHOST or localhost)")
	fs.StringVar(&c.Port, "p", "", "database server port (default $PGPORT or 5432)")
	fs.StringVar(&c.User, "U", "", "database user name (default $PGUSER)")
	fs.StringVar(&c.DBName, "d", "", "database name (default $PGDATABASE)")
	fs.BoolVar(&c.NoPassword, "w", false, "never prompt for password")
	fs.BoolVar(&c.ForcePass, "W", false, "force password prompt")
	fs.StringVar(&c.APIKey, "api-key", "", "TypeSafe API key (default $TYPESAFE_API_KEY)")
	fs.StringVar(&c.APIURL, "api-url", envOr("TYPESAFE_API_URL", defaultAPIURL), "TypeSafe endpoint")
	fs.StringVar(&c.Model, "model", envOr("JEV_MODEL", "jev-latest"), "TypeSafe model")
	thr := 0.5
	if v := os.Getenv("JEV_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			thr = f
		}
	}
	fs.Float64Var(&c.Threshold, "threshold", thr, "default jev() threshold (or $JEV_THRESHOLD)")
	fs.IntVar(&c.BatchSize, "batch-size", 40, "rows per TypeSafe request")
	fs.IntVar(&c.Concurrency, "concurrency", 6, "concurrent TypeSafe requests")
	fs.IntVar(&c.MaxRows, "max-rows", 2500, "abort before any HTTP call if collect exceeds this many rows (0 = off)")
	fs.IntVar(&c.MaxChars, "max-chars", 0, "abort if row objects exceed this many chars in total (0 = off)")
	fs.StringVar(&c.CachePath, "cache", cache.DefaultPath(), "answer cache path")
	fs.BoolVar(&c.NoCache, "no-cache", false, "disable the answer cache")
	fs.BoolVar(&c.Explain, "explain", false, "print the plan and cost estimate; make no TypeSafe calls")
	fs.BoolVar(&c.Timing, "timing", false, "print elapsed time like psql \\timing")
	fs.BoolVar(&c.CSV, "csv", false, "CSV output")
	fs.BoolVar(&c.JSON, "json", false, "JSON output")
	fs.BoolVar(&c.Expanded, "expanded", false, "expanded output like \\x")
	fs.BoolVar(&c.Expanded, "x", false, "expanded output like \\x")
	fs.BoolVar(&c.Verbose, "v", false, "verbose: batches, tokens, cost")
	fs.StringVar(&c.Columns, "columns", "", "comma-separated columns to send for alias-form jev(alias, ...)")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "jevpsql %s - psql-shaped client that evaluates jev() with TypeSafe\n\n", version)
		fmt.Fprintln(stderr, "Usage:\n  jevpsql [flags] [dbname | postgres://...]\n\nFlags:")
		fs.PrintDefaults()
		fmt.Fprintln(stderr, "\nEnvironment: DATABASE_URL, PGHOST/PGPORT/PGUSER/PGPASSWORD/PGDATABASE, TYPESAFE_API_KEY, TYPESAFE_API_URL, JEV_THRESHOLD")
	}
	flags, positional := splitArgs(fs, args)
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	c.Positional = append(positional, fs.Args()...)
	if c.APIKey == "" {
		c.APIKey = os.Getenv("TYPESAFE_API_KEY")
	}
	if c.CSV && c.JSON {
		return nil, errors.New("--csv and --json are mutually exclusive")
	}
	return c, nil
}

// splitArgs lets flags appear after positional arguments, like psql:
// jevpsql "postgres://..." -c "SELECT 1".
func splitArgs(fs *flag.FlagSet, args []string) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // let fs.Parse report it
		}
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positional
}

// configPath is ~/.config/jevpsql/env: KEY=VALUE lines used as defaults
// for environment variables that are not already set.
func configPath() string {
	if p := os.Getenv("JEVPSQL_CONFIG"); p != "" {
		return p
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(base, "jevpsql", "env")
}

// loadConfigFile applies saved defaults to the environment.
func loadConfigFile() {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), "'\"")
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

// saveConfigFile writes KEY=VALUE pairs with mode 0600.
func saveConfigFile(kv map[string]string) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	existing := map[string]string{}
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				existing[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	for k, v := range kv {
		existing[k] = v
	}
	var b strings.Builder
	b.WriteString("# jevpsql defaults; environment variables override these.\n")
	for k, v := range existing {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// Main runs the CLI and returns the exit code.
func Main(args []string, stdin *os.File, stdout, stderr *os.File) int {
	// Never query the terminal for its background colour (OSC 11); it blocks
	// on terminals that do not answer and we do not need it.
	lipgloss.SetHasDarkBackground(true)
	loadConfigFile()
	cfg, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitSQL
	}
	if cfg.ShowVersion {
		fmt.Fprintf(stdout, "jevpsql %s\n", version)
		return ExitOK
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	u := &ui{out: stdout, err: stderr, color: isTerminal(stdout), colorErr: isTerminal(stderr)}
	if cfg.CSV || cfg.JSON {
		u.color = false
	}
	interactive := isTerminal(stdin) && cfg.Command == "" && cfg.File == ""
	toSave := map[string]string{}
	if interactive && !hasConnectionInfo(cfg) {
		fmt.Fprintln(stderr, u.style(dimStyle, "No database configured (use a postgres:// URL, -h/-d flags, DATABASE_URL or PG* env).", u.colorErr))
		url, err := promptLine(stdin, stderr, u.style(accentStyle, "Database URL: ", u.colorErr))
		if err != nil || url == "" {
			u.errorf("no database URL given")
			return ExitSQL
		}
		cfg.Positional = append(cfg.Positional, url)
		toSave["DATABASE_URL"] = url
	}
	if interactive && cfg.APIKey == "" && !cfg.Explain {
		fmt.Fprintln(stderr, u.style(dimStyle, "No TypeSafe API key found (TYPESAFE_API_KEY or --api-key). It is needed only for jev() queries.", u.colorErr))
		key, err := promptSecret(stdin, stderr, u.style(accentStyle, "TypeSafe API key (Enter to skip): ", u.colorErr))
		if err == nil && key != "" {
			cfg.APIKey = key
			toSave["TYPESAFE_API_KEY"] = key
		}
	}
	if len(toSave) > 0 {
		ans, _ := promptLine(stdin, stderr, u.style(accentStyle, "Save these to "+configPath()+" for next time? [y/N] ", u.colorErr))
		if strings.HasPrefix(strings.ToLower(ans), "y") {
			if err := saveConfigFile(toSave); err != nil {
				u.warnf("could not save config: %v", err)
			} else {
				u.notef("saved (mode 0600); delete the file or set the env vars to change them")
			}
		}
	}
	s, err := newSession(ctx, cfg, u, stdin)
	if err != nil {
		u.errorf("%v", err)
		return ExitSQL
	}
	defer s.close()

	switch {
	case cfg.Command != "":
		return s.runScript(ctx, cfg.Command)
	case cfg.File != "":
		data, err := os.ReadFile(cfg.File)
		if err != nil {
			u.errorf("%v", err)
			return ExitSQL
		}
		return s.runScript(ctx, string(data))
	}
	if !isTerminal(stdin) {
		data, err := io.ReadAll(stdin)
		if err != nil {
			u.errorf("%v", err)
			return ExitSQL
		}
		return s.runScript(ctx, string(data))
	}
	return s.repl(ctx)
}

// session is one connected CLI.
type session struct {
	cfg         *Config
	ui          *ui
	conn        *pgx.Conn
	cache       *cache.Store
	ex          *exec.Executor
	timing      bool
	expanded    bool
	format      psqlout.Format
	vars        map[string]string
	interactive bool
	stdin       *os.File
	dbname      string
}

func newSession(ctx context.Context, cfg *Config, u *ui, stdin *os.File) (*session, error) {
	connCfg, err := buildConnConfig(cfg)
	if err != nil {
		return nil, err
	}
	connCfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connCfg.RuntimeParams["application_name"] = "jevpsql"
	connCfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		fmt.Fprintf(u.err, "%s:  %s\n", n.Severity, n.Message)
	}
	if cfg.ForcePass && !cfg.NoPassword {
		pw, err := promptPassword(stdin, u.err)
		if err != nil {
			return nil, err
		}
		connCfg.Password = pw
	}
	conn, err := pgx.ConnectConfig(ctx, connCfg)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "28P01" && !cfg.NoPassword && !cfg.ForcePass && isTerminal(stdin) {
			pw, perr := promptPassword(stdin, u.err)
			if perr != nil {
				return nil, perr
			}
			connCfg.Password = pw
			conn, err = pgx.ConnectConfig(ctx, connCfg)
		}
		if err != nil {
			return nil, fmt.Errorf("connection to server failed: %v", err)
		}
	}
	s := &session{cfg: cfg, ui: u, conn: conn, timing: cfg.Timing, expanded: cfg.Expanded, vars: map[string]string{}, stdin: stdin, dbname: connCfg.Database}
	s.interactive = isTerminal(stdin) && cfg.Command == "" && cfg.File == ""
	switch {
	case cfg.CSV:
		s.format = psqlout.CSV
	case cfg.JSON:
		s.format = psqlout.JSON
	}
	if !cfg.NoCache {
		s.cache, err = cache.Open(cfg.CachePath)
		if err != nil {
			u.warnf("cache disabled: %v", err)
			s.cache = nil
		}
	}
	ts := typesafe.New(cfg.APIURL, cfg.APIKey, cfg.Model, cfg.BatchSize, cfg.Concurrency)
	var cols []string
	if cfg.Columns != "" {
		for _, c := range strings.Split(cfg.Columns, ",") {
			if c = strings.TrimSpace(c); c != "" {
				cols = append(cols, c)
			}
		}
	}
	s.ex = &exec.Executor{DB: conn, TS: ts, Cache: s.cache, Opts: exec.Options{
		Threshold: cfg.Threshold, BatchSize: cfg.BatchSize, Concurrency: cfg.Concurrency,
		MaxRows: cfg.MaxRows, MaxChars: cfg.MaxChars, Columns: cols, Explain: cfg.Explain, Model: cfg.Model,
	}}
	if cfg.Verbose {
		s.ex.Log = func(format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			if strings.HasPrefix(msg, "collect: SELECT") || strings.HasPrefix(msg, "collect: WITH") {
				msg = "collect: " + u.sql(strings.TrimPrefix(msg, "collect: "), u.colorErr)
			}
			u.notef("jev: %s", msg)
		}
	}
	return s, nil
}

func (s *session) close() {
	if s.cache != nil {
		s.cache.Close()
	}
	if s.conn != nil {
		s.conn.Close(context.Background())
	}
}

// promptLine asks for a visible value on the terminal.
func promptLine(stdin *os.File, stderr io.Writer, label string) (string, error) {
	fmt.Fprint(stderr, label)
	r := bufio.NewReader(stdin)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// promptSecret asks for a hidden value on the terminal.
func promptSecret(stdin *os.File, stderr io.Writer, label string) (string, error) {
	fmt.Fprint(stderr, label)
	b, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func promptPassword(stdin *os.File, stderr io.Writer) (string, error) {
	fmt.Fprint(stderr, "Password: ")
	b, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func isURI(s string) bool {
	return strings.HasPrefix(s, "postgres://") || strings.HasPrefix(s, "postgresql://")
}

func buildConnConfig(cfg *Config) (*pgx.ConnConfig, error) {
	var base string
	var dbname string
	if len(cfg.Positional) > 1 {
		return nil, fmt.Errorf("too many arguments: %v", cfg.Positional[1:])
	}
	if len(cfg.Positional) == 1 {
		if isURI(cfg.Positional[0]) {
			base = cfg.Positional[0]
		} else {
			dbname = cfg.Positional[0]
		}
	}
	explicit := cfg.Host != "" || cfg.Port != "" || cfg.User != "" || cfg.DBName != "" || dbname != ""
	if base == "" && !explicit {
		if v := os.Getenv("DATABASE_URL"); v != "" {
			base = v
		}
	}
	if base == "" && os.Getenv("PGHOST") == "" && cfg.Host == "" {
		// psql defaults to a unix socket; keep that but make the URL form easy.
		base = ""
	}
	if base != "" {
		if _, err := url.Parse(base); err != nil {
			return nil, fmt.Errorf("bad connection URI: %w", err)
		}
	}
	c, err := pgx.ParseConfig(base)
	if err != nil {
		return nil, err
	}
	if cfg.Host != "" {
		c.Host = cfg.Host
		if !strings.HasPrefix(cfg.Host, "/") {
			// TLS may have been configured for a socket; let pgx redo it.
			c.TLSConfig = nil
		}
	}
	if cfg.Port != "" {
		p, err := strconv.Atoi(cfg.Port)
		if err != nil {
			return nil, fmt.Errorf("bad port %q", cfg.Port)
		}
		c.Port = uint16(p)
	}
	if cfg.User != "" {
		c.User = cfg.User
	}
	if cfg.DBName != "" {
		c.Database = cfg.DBName
	} else if dbname != "" {
		c.Database = dbname
	}
	if c.Host != "" && !strings.HasPrefix(c.Host, "/") && c.TLSConfig == nil && base == "" && os.Getenv("PGSSLMODE") == "" {
		// Match psql: try TLS first (sslmode=prefer) for TCP hosts.
		cfg2, err := pgx.ParseConfig(fmt.Sprintf("host=%s port=%d sslmode=prefer", c.Host, c.Port))
		if err == nil {
			c.TLSConfig = cfg2.TLSConfig
			c.Fallbacks = cfg2.Fallbacks
		}
	}
	return c, nil
}

// hasConnectionInfo reports whether anything tells us where the database is.
func hasConnectionInfo(cfg *Config) bool {
	if len(cfg.Positional) > 0 || cfg.Host != "" || cfg.DBName != "" || cfg.User != "" || cfg.Port != "" {
		return true
	}
	for _, k := range []string{"DATABASE_URL", "PGHOST", "PGDATABASE", "PGSERVICE"} {
		if os.Getenv(k) != "" {
			return true
		}
	}
	return false
}

// exitFor maps an error to an exit code.
func exitFor(err error) int {
	var ae *typesafe.APIError
	var be *exec.BudgetError
	if errors.As(err, &ae) || errors.As(err, &be) {
		return ExitAPI
	}
	return ExitSQL
}

// runScript executes a -c / -f / piped script and returns the exit code.
func (s *session) runScript(ctx context.Context, script string) int {
	code := ExitOK
	for _, line := range splitScript(script) {
		if ctx.Err() != nil {
			return ExitSQL
		}
		if strings.HasPrefix(strings.TrimSpace(line), "\\") {
			if quit, err := s.meta(ctx, strings.TrimSpace(line)); err != nil {
				s.ui.errorf("%v", err)
				code = ExitSQL
			} else if quit {
				return code
			}
			continue
		}
		if err := s.runStatement(ctx, line); err != nil {
			c := exitFor(err)
			if c > code {
				code = c
			}
			if c == ExitAPI {
				return code
			}
		}
	}
	return code
}

// splitScript separates meta-commands (one per line, only between
// statements) from SQL statements (split on top-level semicolons).
func splitScript(script string) []string {
	var out []string
	var sqlBuf strings.Builder
	flush := func() {
		if strings.TrimSpace(sqlBuf.String()) == "" {
			sqlBuf.Reset()
			return
		}
		stmts, err := parse.Split(sqlBuf.String())
		if err != nil {
			out = append(out, sqlBuf.String())
		} else {
			out = append(out, stmts...)
		}
		sqlBuf.Reset()
	}
	for _, line := range strings.Split(script, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "\\") && strings.TrimSpace(sqlBuf.String()) == "" {
			out = append(out, t)
			continue
		}
		sqlBuf.WriteString(line)
		sqlBuf.WriteString("\n")
		if strings.HasSuffix(t, ";") {
			flush()
		}
	}
	flush()
	return out
}

// runStatement executes one SQL statement and prints the result.
func (s *session) runStatement(ctx context.Context, sql string) error {
	sql = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(sql), ";"))
	if sql == "" {
		return nil
	}
	start := time.Now()
	var prog *progressUI
	needsJev := false
	if st, err := parse.ParseOne(sql); err == nil && st.HasJev {
		needsJev = true
	}
	if needsJev && !s.cfg.Explain {
		if s.cfg.APIKey == "" && s.interactive {
			key, err := promptSecret(s.stdin, s.ui.err, s.ui.style(accentStyle, "TypeSafe API key: ", s.ui.colorErr))
			if err == nil && key != "" {
				s.setAPIKey(key)
			}
		}
		if s.cfg.APIKey == "" {
			err := &typesafe.APIError{Status: 0, Body: "no API key: set TYPESAFE_API_KEY or pass --api-key"}
			s.ui.errorf("%v", err)
			return err
		}
		prog = startProgress(s.ui.err.(*os.File), "collecting rows")
		s.ex.Progress = prog.update
	}
	res, err := s.ex.Run(ctx, sql)
	prog.stop()
	elapsed := time.Since(start)
	if err != nil {
		if res != nil && res.Stats != nil && (s.cfg.Verbose || s.interactive) {
			s.ui.footer(res.Stats)
		}
		s.printError(err)
		return err
	}
	switch {
	case res.Explain != nil:
		x := res.Explain
		fmt.Fprintf(s.ui.out, "%s\n  %s\n\n", s.ui.style(titleStyle, "collect SQL:", s.ui.color), s.ui.sql(x.CollectSQL, s.ui.color))
		body := x.Render()
		body = body[strings.Index(body, "rows after"):]
		fmt.Fprint(s.ui.out, body)
	case res.Table != nil:
		opts := psqlout.Options{Format: s.format, Expanded: s.expanded, Color: s.ui.color}
		if err := psqlout.Write(s.ui.out, res.Table, opts); err != nil {
			return err
		}
	default:
		fmt.Fprintln(s.ui.out, res.Tag)
	}
	if res.Stats != nil && (s.cfg.Verbose || s.interactive) {
		s.ui.footer(res.Stats)
	}
	if s.timing {
		fmt.Fprintf(s.ui.out, "Time: %.3f ms\n", float64(elapsed.Microseconds())/1000)
	}
	return nil
}

func (s *session) setAPIKey(key string) {
	s.cfg.APIKey = key
	s.ex.TS.APIKey = key
}

func (s *session) printError(err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		s.ui.errorf("%s", pgErr.Message)
		if pgErr.Detail != "" {
			fmt.Fprintf(s.ui.err, "DETAIL:  %s\n", pgErr.Detail)
		}
		if pgErr.Hint != "" {
			fmt.Fprintf(s.ui.err, "HINT:  %s\n", pgErr.Hint)
		}
		if strings.Contains(err.Error(), "collect query failed") {
			// Show the rewritten SQL so the user can see what the server saw.
			if i := strings.Index(err.Error(), "SQL: "); i >= 0 {
				fmt.Fprintf(s.ui.err, "COLLECT SQL:  %s\n", s.ui.sql(err.Error()[i+5:], s.ui.colorErr))
			}
		}
		return
	}
	if errors.Is(err, context.Canceled) {
		s.ui.errorf("canceling statement due to user request")
		return
	}
	s.ui.errorf("%v", err)
}

// historyPath is where the REPL keeps its history.
func historyPath() string {
	return filepath.Join(filepath.Dir(cache.DefaultPath()), "history")
}
