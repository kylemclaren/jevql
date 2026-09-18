// Package wire defines the JSON documents shared by `jevql serve`,
// `jevql --json-table` and the SDKs. See sdk/PROTOCOL.md.
package wire

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kylemclaren/jevql/internal/canon"
	"github.com/kylemclaren/jevql/internal/exec"
	"github.com/kylemclaren/jevql/internal/typesafe"
)

// QueryRequest is the body of POST /v1/query.
type QueryRequest struct {
	SQL       string   `json:"sql"`
	Threshold *float64 `json:"threshold,omitempty"`
	Explain   bool     `json:"explain,omitempty"`
	MaxRows   *int     `json:"max_rows,omitempty"`
}

// Stats mirrors stats.Stats with JSON names.
type Stats struct {
	CollectRows  int     `json:"collect_rows"`
	Judged       int     `json:"judged"`
	Requests     int     `json:"requests"`
	CacheHits    int     `json:"cache_hits"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	USD          float64 `json:"usd"`
	ElapsedMS    int64   `json:"elapsed_ms"`
}

// Explain mirrors exec.Explain with JSON names.
type Explain struct {
	CollectSQL   string   `json:"collect_sql"`
	Rows         int64    `json:"rows"`
	Sources      int      `json:"sources"`
	Questions    int      `json:"questions"`
	Judgements   int      `json:"judgements"`
	Batches      int      `json:"batches"`
	AvgRowChars  float64  `json:"avg_row_chars"`
	Tokens       int      `json:"tokens"`
	USD          float64  `json:"usd"`
	ServerOrder  bool     `json:"server_order"`
	QuestionList []string `json:"question_list"`
}

// QueryResult is one statement's outcome.
type QueryResult struct {
	Columns  []string `json:"columns"`
	Rows     [][]any  `json:"rows"`
	RowCount int      `json:"row_count"`
	Tag      string   `json:"tag"`
	Jev      bool     `json:"jev"`
	Stats    *Stats   `json:"stats"`
	Explain  *Explain `json:"explain"`
}

// ErrorBody is the JSON error envelope.
type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// FromResult converts an executor result into the wire document.
func FromResult(r *exec.Result) *QueryResult {
	out := &QueryResult{Columns: []string{}, Rows: [][]any{}, Tag: r.Tag, Jev: r.Jev}
	if r.Table != nil {
		out.Columns = append(out.Columns, r.Table.Columns...)
		for _, row := range r.Table.Rows {
			vals := make([]any, len(r.Table.Columns))
			for i := range r.Table.Columns {
				if i >= len(row) || row[i].Null {
					vals[i] = nil
					continue
				}
				if row[i].Value != nil {
					vals[i] = canon.Value(row[i].Value)
				} else {
					vals[i] = row[i].Text
				}
			}
			out.Rows = append(out.Rows, vals)
		}
		out.RowCount = len(out.Rows)
		if out.Tag == "" {
			out.Tag = "SELECT " + itoa(out.RowCount)
		}
	}
	if r.Stats != nil {
		out.Stats = &Stats{CollectRows: r.Stats.CollectRows, Judged: r.Stats.Judged, Requests: r.Stats.Requests,
			CacheHits: r.Stats.CacheHits, InputTokens: r.Stats.InputTokens, OutputTokens: r.Stats.OutputTokens,
			USD: r.Stats.USD, ElapsedMS: r.Stats.Elapsed.Milliseconds()}
	}
	if r.Explain != nil {
		x := r.Explain
		out.Explain = &Explain{CollectSQL: x.CollectSQL, Rows: x.Rows, Sources: x.Sources, Questions: x.Questions,
			Judgements: x.Judgements, Batches: x.Batches, AvgRowChars: x.AvgRowChars, Tokens: x.Tokens, USD: x.USD,
			ServerOrder: x.ServerOrder, QuestionList: append([]string{}, x.QuestionList...)}
		if out.Explain.QuestionList == nil {
			out.Explain.QuestionList = []string{}
		}
	}
	return out
}

// Classify maps an error to a protocol code and HTTP status.
func Classify(err error) (code string, status int) {
	var ae *typesafe.APIError
	var be *exec.BudgetError
	var pe *pgconn.PgError
	switch {
	case errors.As(err, &be):
		return "budget", 402
	case errors.As(err, &ae):
		return "api", 502
	case errors.As(err, &pe):
		return "sql", 400
	}
	return "sql", 400
}

// ErrorFor builds the error body for err.
func ErrorFor(err error) (ErrorBody, int) {
	code, status := Classify(err)
	msg := err.Error()
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		msg = pe.Message
		if pe.Detail != "" {
			msg += " (" + pe.Detail + ")"
		}
	}
	return ErrorBody{Error: msg, Code: code}, status
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

var _ = http.StatusOK
