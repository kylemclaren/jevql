package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kylemclaren/jevql/internal/cache"
	"github.com/kylemclaren/jevql/internal/canon"
	"github.com/kylemclaren/jevql/internal/stats"
	"github.com/kylemclaren/jevql/internal/typesafe"
)

// JudgeRows judges caller-supplied rows with one question, sharing the
// batching, dedup and cache of the SQL path. Answers come back in input order.
func (e *Executor) JudgeRows(ctx context.Context, req JudgeRequest) (*JudgeResult, error) {
	start := time.Now()
	if strings.TrimSpace(req.Question) == "" {
		return nil, errors.New("question is required")
	}
	kind := typesafe.Kind(strings.ToLower(strings.TrimSpace(req.Kind)))
	if kind == "" {
		kind = typesafe.Noul
	}
	switch kind {
	case typesafe.Noul:
	case typesafe.Choice:
		if len(req.Options) == 0 {
			return nil, errors.New("choice needs at least one option")
		}
	case typesafe.Score:
		if len(req.Options) < 2 {
			return nil, errors.New("score needs at least two levels")
		}
	default:
		return nil, fmt.Errorf("unknown kind %q (want noul, choice or score)", req.Kind)
	}
	if len(req.Rows) == 0 {
		return &JudgeResult{Answers: []JudgeAnswer{}, Stats: &JudgeStats{}}, nil
	}
	if e.Opts.MaxRows > 0 && len(req.Rows) > e.Opts.MaxRows {
		return nil, &BudgetError{Msg: fmt.Sprintf("%d rows is more than --max-rows (%d)", len(req.Rows), e.Opts.MaxRows)}
	}
	q := typesafe.Question{Kind: kind, Text: req.Question, Options: req.Options}
	thr := e.Opts.Threshold
	if req.Threshold != nil {
		thr = *req.Threshold
	}

	// Canonicalise and dedup.
	st := &stats.Stats{CollectRows: len(req.Rows)}
	items := map[string]*typesafe.Item{}
	var order []*typesafe.Item
	perRow := make([]*typesafe.Item, len(req.Rows))
	total := 0
	for i, row := range req.Rows {
		b, err := canon.Marshal(row)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		total += len(b)
		k := string(b)
		it, ok := items[k]
		if !ok {
			it = &typesafe.Item{Q: q, Row: b}
			items[k] = it
			order = append(order, it)
		}
		perRow[i] = it
	}
	if e.Opts.MaxChars > 0 && total > e.Opts.MaxChars {
		return nil, &BudgetError{Msg: fmt.Sprintf("row objects total %d chars, more than --max-chars (%d)", total, e.Opts.MaxChars)}
	}
	st.Judged = len(order)

	// Cache, then HTTP for the misses.
	keys := make([]string, len(order))
	for i, it := range order {
		keys[i] = cache.Key(e.TS.Model, string(kind), q.Text, q.Options, it.Row)
	}
	hits, err := e.Cache.GetMany(ctx, keys)
	if err != nil {
		e.logf("cache read failed: %v", err)
		hits = nil
	}
	var pending []*typesafe.Item
	for i, it := range order {
		if raw, ok := hits[keys[i]]; ok {
			if ans, err := typesafe.ParseAnswer(raw); err == nil && ans.Type == string(kind) {
				it.Answer = ans
				st.CacheHits++
				continue
			}
		}
		pending = append(pending, it)
	}
	if len(pending) > 0 {
		usage, err := e.TS.Judge(ctx, pending, e.Progress)
		st.Requests += usage.Requests
		st.InputTokens += usage.InputTokens
		st.OutputTokens += usage.OutputTokens
		st.USD += usage.USD()
		if err != nil {
			return nil, err
		}
		put := map[string]json.RawMessage{}
		for i, it := range order {
			if _, hit := hits[keys[i]]; !hit && it.Answer != nil {
				put[keys[i]] = it.Answer.Raw
			}
		}
		if err := e.Cache.PutMany(ctx, put); err != nil {
			e.logf("cache write failed: %v", err)
		}
	}

	out := &JudgeResult{Answers: make([]JudgeAnswer, len(req.Rows))}
	for i, it := range perRow {
		a := it.Answer
		if a == nil {
			return nil, errors.New("typesafe: missing answer after judge")
		}
		ja := JudgeAnswer{}
		switch kind {
		case typesafe.Noul:
			p := a.P()
			pass := p >= thr
			ja.P, ja.Pass = &p, &pass
			c := a.ConfidenceValue()
			ja.Confidence = &c
		case typesafe.Choice:
			ja.Choice = a.Choice
			c := a.Confidence
			ja.Confidence = &c
			ja.Probabilities = a.Probabilities
		case typesafe.Score:
			s := a.Score
			n := s / float64(len(req.Options)-1)
			c := a.Confidence
			ja.Score, ja.Norm, ja.Confidence = &s, &n, &c
			ja.Probabilities = a.Probabilities
		}
		if req.Raw {
			ja.Raw = a.Raw
		}
		out.Answers[i] = ja
	}
	st.Elapsed = time.Since(start)
	out.Stats = &JudgeStats{CollectRows: st.CollectRows, Judged: st.Judged, Requests: st.Requests, CacheHits: st.CacheHits,
		InputTokens: st.InputTokens, OutputTokens: st.OutputTokens, USD: st.USD, ElapsedMS: st.Elapsed.Milliseconds()}
	return out, nil
}
