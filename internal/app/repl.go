package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ergochat/readline"
	"github.com/jackc/pgx/v5"

	"github.com/kylemclaren/jevpsql/internal/parse"
	"github.com/kylemclaren/jevpsql/internal/psqlout"
)

func (s *session) repl(ctx context.Context) int {
	_ = os.MkdirAll(filepath.Dir(historyPath()), 0o700)
	rl, err := readline.NewFromConfig(&readline.Config{
		Prompt:          s.prompt(false),
		HistoryFile:     historyPath(),
		HistoryLimit:    5000,
		InterruptPrompt: "^C",
		EOFPrompt:       "\\q",
		Stdin:           s.stdin,
		Stdout:          s.ui.out,
		Stderr:          s.ui.err,
	})
	if err != nil {
		s.ui.errorf("%v", err)
		return ExitSQL
	}
	defer rl.Close()
	fmt.Fprintf(s.ui.out, "jevpsql %s (%s)\nType \"help\" for help.\n\n", version, s.serverVersion(ctx))
	var buf strings.Builder
	for {
		if ctx.Err() != nil {
			return ExitOK
		}
		rl.SetPrompt(s.prompt(buf.Len() > 0))
		line, err := rl.ReadLine()
		if errors.Is(err, readline.ErrInterrupt) {
			buf.Reset()
			continue
		}
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(s.ui.out, "\\q")
			return ExitOK
		}
		if err != nil {
			s.ui.errorf("%v", err)
			return ExitSQL
		}
		trimmed := strings.TrimSpace(line)
		if buf.Len() == 0 {
			if trimmed == "" {
				continue
			}
			if trimmed == "help" || trimmed == "\\?" || trimmed == "\\h" {
				s.help()
				continue
			}
			if strings.HasPrefix(trimmed, "\\") {
				quit, err := s.meta(ctx, trimmed)
				if err != nil {
					s.ui.errorf("%v", err)
				}
				if quit {
					return ExitOK
				}
				continue
			}
		}
		buf.WriteString(line)
		buf.WriteString("\n")
		if !strings.HasSuffix(trimmed, ";") {
			continue
		}
		script := buf.String()
		buf.Reset()
		stmts, err := parse.Split(script)
		if err != nil {
			stmts = []string{script}
		}
		for _, st := range stmts {
			_ = s.runStatement(ctx, st)
		}
	}
}

func (s *session) prompt(cont bool) string {
	db := s.dbname
	if db == "" {
		db = "jevpsql"
	}
	if cont {
		return db + "-> "
	}
	return s.ui.style(accentStyle, db, s.ui.color) + "=> "
}

func (s *session) serverVersion(ctx context.Context) string {
	var v string
	if err := s.conn.QueryRow(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "postgres"
	}
	return "PostgreSQL " + v
}

func (s *session) help() {
	fmt.Fprint(s.ui.out, `jevpsql: psql-shaped client that evaluates jev() with TypeSafe.

SQL functions (evaluated here, never on the server):
  jev(alias, 'condition' [, threshold])           boolean
  jev_prob(alias, 'condition')                    float8 0..1
  jev_choice(alias, 'question', ARRAY['a','b'])   text
  jev_score(alias, 'question', ARRAY['lo','hi'])  float8 weighted level index
  jev_score_norm(...)                              0..1
  jev_confidence(alias, 'q' [, 'noul'|'choice'|'score', ARRAY[...]])
  jev_eval(alias, 'q' [, kind, ARRAY[...]])       raw answer JSON
  Use jev((col1, col2), ...) to send only some columns.

Meta commands:
  \q                quit               \timing [on|off]   toggle timing
  \x [on|off]       expanded output    \d [name]          describe
  \dt \dn \l        tables/schemas/dbs \set JEV_THRESHOLD 0.7
  \cache [stats|clear]                 \explain <query>   plan, no HTTP
  \set TYPESAFE_API_KEY tsk_...        \set JEV_MODEL jev-preview
  \i file           run a file         help               this text
`)
}

// meta runs a backslash command. It returns quit=true for \q.
func (s *session) meta(ctx context.Context, line string) (bool, error) {
	fields := strings.Fields(line)
	cmd := fields[0]
	args := fields[1:]
	onoff := func(cur bool) bool {
		if len(args) == 0 {
			return !cur
		}
		return strings.EqualFold(args[0], "on") || args[0] == "1" || strings.EqualFold(args[0], "true")
	}
	switch cmd {
	case "\\q", "\\quit":
		return true, nil
	case "\\timing":
		s.timing = onoff(s.timing)
		if s.timing {
			fmt.Fprintln(s.ui.out, "Timing is on.")
		} else {
			fmt.Fprintln(s.ui.out, "Timing is off.")
		}
	case "\\x":
		s.expanded = onoff(s.expanded)
		if s.expanded {
			fmt.Fprintln(s.ui.out, "Expanded display is on.")
		} else {
			fmt.Fprintln(s.ui.out, "Expanded display is off.")
		}
	case "\\?", "\\h", "help":
		s.help()
	case "\\set":
		if len(args) == 0 {
			for k, v := range s.vars {
				fmt.Fprintf(s.ui.out, "%s = '%s'\n", k, v)
			}
			fmt.Fprintf(s.ui.out, "JEV_THRESHOLD = '%g'\n", s.ex.Opts.Threshold)
			return false, nil
		}
		if len(args) < 2 {
			return false, errors.New("\\set NAME VALUE")
		}
		val := strings.Trim(strings.Join(args[1:], " "), "'\"")
		switch strings.ToUpper(args[0]) {
		case "JEV_THRESHOLD":
			f, err := strconv.ParseFloat(val, 64)
			if err != nil || f < 0 || f > 1 {
				return false, fmt.Errorf("JEV_THRESHOLD must be a number between 0 and 1")
			}
			s.ex.Opts.Threshold = f
		case "TYPESAFE_API_KEY":
			s.setAPIKey(val)
			fmt.Fprintln(s.ui.out, "TypeSafe API key set.")
		case "JEV_MODEL":
			s.ex.TS.Model = val
			s.ex.Opts.Model = val
		case "JEV_BATCH_SIZE":
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 {
				return false, fmt.Errorf("JEV_BATCH_SIZE must be a positive integer")
			}
			s.ex.TS.BatchSize = n
			s.ex.Opts.BatchSize = n
		case "JEV_MAX_ROWS":
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return false, fmt.Errorf("JEV_MAX_ROWS must be a non-negative integer")
			}
			s.ex.Opts.MaxRows = n
		default:
			s.vars[args[0]] = val
		}
	case "\\cache":
		sub := "stats"
		if len(args) > 0 {
			sub = args[0]
		}
		switch sub {
		case "stats":
			fmt.Fprintln(s.ui.out, s.cache.Describe(ctx))
		case "clear":
			n, err := s.cache.Clear(ctx)
			if err != nil {
				return false, err
			}
			fmt.Fprintf(s.ui.out, "cache: cleared %d entries\n", n)
		default:
			return false, errors.New("\\cache [stats|clear]")
		}
	case "\\explain":
		q := strings.TrimSpace(strings.TrimPrefix(line, "\\explain"))
		if q == "" {
			return false, errors.New("\\explain <query>")
		}
		prev := s.ex.Opts.Explain
		s.ex.Opts.Explain = true
		_ = s.runStatement(ctx, q)
		s.ex.Opts.Explain = prev
	case "\\i", "\\include":
		if len(args) != 1 {
			return false, errors.New("\\i FILE")
		}
		data, err := os.ReadFile(args[0])
		if err != nil {
			return false, err
		}
		s.runScript(ctx, string(data))
	case "\\d", "\\dt", "\\dn", "\\l", "\\d+", "\\dt+", "\\dv", "\\ds", "\\di":
		return false, s.describe(ctx, cmd, args)
	case "\\c", "\\connect":
		return false, errors.New("\\c is not implemented; restart jevpsql with a new connection string")
	default:
		if strings.HasPrefix(cmd, "\\d") {
			return false, s.describe(ctx, cmd, args)
		}
		return false, fmt.Errorf("%s: not implemented", cmd)
	}
	return false, nil
}

func (s *session) queryTable(ctx context.Context, sql string, args ...any) error {
	res, err := s.ex.Run(ctx, formatArgs(sql, args...))
	if err != nil {
		return err
	}
	if res.Table == nil {
		return nil
	}
	return psqlout.Write(s.ui.out, res.Table, psqlout.Options{Format: s.format, Expanded: s.expanded, Color: s.ui.color})
}

// formatArgs inlines string arguments as SQL literals (catalog queries only).
func formatArgs(sql string, args ...any) string {
	for i, a := range args {
		lit := "'" + strings.ReplaceAll(fmt.Sprint(a), "'", "''") + "'"
		sql = strings.ReplaceAll(sql, fmt.Sprintf("$%d", i+1), lit)
	}
	return sql
}

func (s *session) describe(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "\\dn":
		return s.queryTable(ctx, `SELECT n.nspname AS "Name", pg_get_userbyid(n.nspowner) AS "Owner" FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema' ORDER BY 1`)
	case "\\l":
		return s.queryTable(ctx, `SELECT d.datname AS "Name", pg_get_userbyid(d.datdba) AS "Owner", pg_encoding_to_char(d.encoding) AS "Encoding" FROM pg_database d WHERE NOT d.datistemplate ORDER BY 1`)
	}
	kinds := "'r','p','v','m','S','f'"
	switch cmd {
	case "\\dt", "\\dt+":
		kinds = "'r','p'"
	case "\\dv":
		kinds = "'v','m'"
	case "\\ds":
		kinds = "'S'"
	case "\\di":
		kinds = "'i','I'"
	}
	if len(args) == 0 || cmd != "\\d" && cmd != "\\d+" {
		pattern := "%"
		if len(args) > 0 {
			pattern = strings.ReplaceAll(args[0], "*", "%")
		}
		return s.queryTable(ctx, `SELECT n.nspname AS "Schema", c.relname AS "Name",
  CASE c.relkind WHEN 'r' THEN 'table' WHEN 'p' THEN 'partitioned table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' WHEN 'S' THEN 'sequence' WHEN 'f' THEN 'foreign table' WHEN 'i' THEN 'index' WHEN 'I' THEN 'partitioned index' END AS "Type",
  pg_get_userbyid(c.relowner) AS "Owner"
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN (`+kinds+`) AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
  AND c.relname LIKE $1 AND pg_table_is_visible(c.oid)
ORDER BY 1, 2`, pattern)
	}
	name := args[0]
	var oid uint32
	var rel string
	var kind string
	err := s.conn.QueryRow(ctx, formatArgs(`SELECT c.oid, n.nspname || '.' || c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.oid = to_regclass($1)`, name)).Scan(&oid, &rel, &kind)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && oid == 0) {
		return fmt.Errorf("Did not find any relation named \"%s\".", name)
	}
	if err != nil {
		return err
	}
	what := "Table"
	switch kind {
	case "v":
		what = "View"
	case "m":
		what = "Materialized view"
	case "S":
		what = "Sequence"
	case "i":
		what = "Index"
	}
	fmt.Fprintf(s.ui.out, "%s %q\n", what, rel)
	if err := s.queryTable(ctx, `SELECT a.attname AS "Column", format_type(a.atttypid, a.atttypmod) AS "Type",
  CASE WHEN a.attnotnull THEN 'not null' ELSE '' END AS "Nullable",
  COALESCE(pg_get_expr(d.adbin, d.adrelid), '') AS "Default"
FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE a.attrelid = `+strconv.Itoa(int(oid))+` AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`); err != nil {
		return err
	}
	rows, err := s.conn.Query(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname || '.' || tablename = `+formatArgs("$1", rel)+` ORDER BY indexname`)
	if err != nil {
		return err
	}
	defer rows.Close()
	first := true
	for rows.Next() {
		var def string
		if err := rows.Scan(&def); err != nil {
			return err
		}
		if first {
			fmt.Fprintln(s.ui.out, "Indexes:")
			first = false
		}
		fmt.Fprintf(s.ui.out, "    %s\n", def)
	}
	return rows.Err()
}
