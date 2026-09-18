// Package typesafe is the HTTP client for TypeSafe's System One endpoint.
// It owns the request/response schema so the rest of the CLI never sees
// it; if the API drifts, only this package changes.
package typesafe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Kind is the question type.
type Kind string

const (
	Noul   Kind = "noul"
	Choice Kind = "choice"
	Score  Kind = "score"
)

// Question is what is asked about each row.
type Question struct {
	Kind    Kind
	Text    string
	Options []string // choice / score
}

// Key identifies a question for batching and caching.
func (q Question) Key() string {
	return string(q.Kind) + "\x00" + q.Text + "\x00" + strings.Join(q.Options, "\x01")
}

// Answer is a parsed TypeSafe answer.
type Answer struct {
	Type          string             `json:"type"`
	Noul          float64            `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        json.RawMessage    `json:"legend,omitempty"`
	Raw           json.RawMessage    `json:"-"`
}

// P is the calibrated probability for a noul answer.
func (a *Answer) P() float64 { return a.Noul }

// ConfidenceValue returns the confidence, deriving it for noul answers.
func (a *Answer) ConfidenceValue() float64 {
	if a.Type == string(Noul) {
		if a.Noul >= 0.5 {
			return a.Noul
		}
		return 1 - a.Noul
	}
	return a.Confidence
}

// ParseAnswer decodes one raw answer object.
func ParseAnswer(raw json.RawMessage) (*Answer, error) {
	var a Answer
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	a.Raw = raw
	return &a, nil
}

// Item is one row to judge against one question.
type Item struct {
	Q      Question
	Row    json.RawMessage // canonical row object
	Answer *Answer
}

// Usage totals across requests.
type Usage struct {
	Requests     int
	InputTokens  int
	OutputTokens int
}

// USD estimates cost at $0.042 per million input tokens.
func (u Usage) USD() float64 { return float64(u.InputTokens) * 0.042 / 1e6 }

// Progress is reported after every completed batch.
type Progress struct {
	BatchesDone  int
	BatchesTotal int
	ItemsDone    int
	ItemsTotal   int
	Usage        Usage
}

// Client talks to POST /v1/systemone.
type Client struct {
	URL         string
	APIKey      string
	Model       string
	BatchSize   int
	Concurrency int
	MaxRetries  int
	HTTP        *http.Client
}

// New builds a client with defaults filled in.
func New(url, key, model string, batch, conc int) *Client {
	if batch <= 0 {
		batch = 40
	}
	if conc <= 0 {
		conc = 6
	}
	return &Client{URL: url, APIKey: key, Model: model, BatchSize: batch, Concurrency: conc, MaxRetries: 4,
		HTTP: &http.Client{Timeout: 120 * time.Second}}
}

// APIError is a non-2xx response.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	b := e.Body
	if len(b) > 400 {
		b = b[:400] + "..."
	}
	return fmt.Sprintf("typesafe: HTTP %d: %s", e.Status, b)
}

type batch struct {
	q     Question
	items []*Item
}

// Batches packs items sharing a question into request-sized groups.
func (c *Client) Batches(items []*Item) []batch {
	byQ := map[string]*batch{}
	var order []string
	for _, it := range items {
		k := it.Q.Key()
		b, ok := byQ[k]
		if !ok {
			b = &batch{q: it.Q}
			byQ[k] = b
			order = append(order, k)
		}
		b.items = append(b.items, it)
	}
	var out []batch
	for _, k := range order {
		b := byQ[k]
		for i := 0; i < len(b.items); i += c.BatchSize {
			end := i + c.BatchSize
			if end > len(b.items) {
				end = len(b.items)
			}
			out = append(out, batch{q: b.q, items: b.items[i:end]})
		}
	}
	return out
}

// BatchCount is how many HTTP requests Judge would make for items.
func (c *Client) BatchCount(items []*Item) int { return len(c.Batches(items)) }

// Judge fills in Answer on every item, making batched concurrent requests.
func (c *Client) Judge(ctx context.Context, items []*Item, progress func(Progress)) (Usage, error) {
	batches := c.Batches(items)
	var mu sync.Mutex
	var usage Usage
	done, itemsDone := 0, 0
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(c.Concurrency)
	for _, b := range batches {
		b := b
		g.Go(func() error {
			u, err := c.judgeBatch(ctx, b)
			if err != nil {
				return err
			}
			mu.Lock()
			usage.Requests += u.Requests
			usage.InputTokens += u.InputTokens
			usage.OutputTokens += u.OutputTokens
			done++
			itemsDone += len(b.items)
			p := Progress{BatchesDone: done, BatchesTotal: len(batches), ItemsDone: itemsDone, ItemsTotal: len(items), Usage: usage}
			mu.Unlock()
			if progress != nil {
				progress(p)
			}
			return nil
		})
	}
	err := g.Wait()
	return usage, err
}

// Request is the wire format of POST /v1/systemone.
type Request struct {
	Model     string              `json:"model"`
	State     map[string]any      `json:"state"`
	Questions map[string]question `json:"questions"`
}

type question struct {
	Type         Kind   `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type stateRow struct {
	I   int             `json:"i"`
	Row json.RawMessage `json:"row"`
}

// Response is the wire format of the answer.
type Response struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// BuildRequest renders one batch as a request body.
func (c *Client) BuildRequest(q Question, rows []json.RawMessage) *Request {
	req := &Request{Model: c.Model, State: map[string]any{}, Questions: map[string]question{}}
	srows := make([]stateRow, len(rows))
	for i, r := range rows {
		srows[i] = stateRow{I: i, Row: r}
	}
	req.State["rows"] = srows
	switch q.Kind {
	case Noul:
		req.State["condition"] = q.Text
	default:
		req.State["question"] = q.Text
	}
	for i := range rows {
		id := fmt.Sprintf("row_%d", i)
		switch q.Kind {
		case Noul:
			req.Questions[id] = question{Type: Noul,
				Instructions: fmt.Sprintf("Does the row in rows with \"i\": %d satisfy the condition? Judge only that row and ignore every other row.", i)}
		case Choice:
			crit := map[string]string{}
			for _, o := range q.Options {
				crit[o] = o
			}
			req.Questions[id] = question{Type: Choice, Criteria: crit,
				Instructions: fmt.Sprintf("Answer the question for the row in rows with \"i\": %d only. Ignore every other row.", i)}
		case Score:
			req.Questions[id] = question{Type: Score, Criteria: q.Options,
				Instructions: fmt.Sprintf("Answer the question for the row in rows with \"i\": %d only. Ignore every other row.", i)}
		}
	}
	return req
}

func (c *Client) judgeBatch(ctx context.Context, b batch) (Usage, error) {
	rows := make([]json.RawMessage, len(b.items))
	for i, it := range b.items {
		rows[i] = it.Row
	}
	body, err := json.Marshal(c.BuildRequest(b.q, rows))
	if err != nil {
		return Usage{}, err
	}
	var resp *Response
	var last error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			d := time.Duration(500*(1<<uint(attempt-1)))*time.Millisecond + time.Duration(rand.Intn(300))*time.Millisecond
			select {
			case <-ctx.Done():
				return Usage{}, ctx.Err()
			case <-time.After(d):
			}
		}
		resp, last = c.post(ctx, body)
		if last == nil {
			break
		}
		var ae *APIError
		if errors.As(last, &ae) && !(ae.Status == 429 || ae.Status >= 500) {
			return Usage{}, last
		}
		if ctx.Err() != nil {
			return Usage{}, ctx.Err()
		}
	}
	if last != nil {
		return Usage{}, last
	}
	for i, it := range b.items {
		raw, ok := resp.Answers[fmt.Sprintf("row_%d", i)]
		if !ok {
			return Usage{}, fmt.Errorf("typesafe: response is missing answer row_%d", i)
		}
		a, err := ParseAnswer(raw)
		if err != nil {
			return Usage{}, fmt.Errorf("typesafe: bad answer row_%d: %w", i, err)
		}
		it.Answer = a
	}
	return Usage{Requests: 1, InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}, nil
}

func (c *Client) post(ctx context.Context, body []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, &APIError{Status: 0, Body: err.Error()}
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, &APIError{Status: res.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	var out Response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("typesafe: decode response: %w", err)
	}
	if out.Answers == nil {
		return nil, fmt.Errorf("typesafe: response has no answers: %s", truncate(string(data), 200))
	}
	return &out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// EstimateTokens is a rough input-token estimate for judging n rows of
// avgChars canonical JSON each with one question, in batches of batchSize.
func EstimateTokens(n int, avgChars float64, batchSize int) int {
	if n == 0 {
		return 0
	}
	if batchSize <= 0 {
		batchSize = 40
	}
	batches := (n + batchSize - 1) / batchSize
	perRow := avgChars/3.5 + 30 // row json + its question
	return int(float64(n)*perRow) + batches*60
}
