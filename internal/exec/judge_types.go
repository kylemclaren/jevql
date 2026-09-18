package exec

import "encoding/json"

// JudgeRequest is the body of POST /v1/judge: rows you already hold, no database.
type JudgeRequest struct {
	Question  string           `json:"question"`
	Kind      string           `json:"kind"` // noul | choice | score (default noul)
	Options   []string         `json:"options,omitempty"`
	Threshold *float64         `json:"threshold,omitempty"`
	Rows      []map[string]any `json:"rows"`
	Raw       bool             `json:"raw,omitempty"` // include the raw TypeSafe answer
}

// JudgeAnswer is one row's answer, in input order.
type JudgeAnswer struct {
	P             *float64           `json:"p,omitempty"`    // noul
	Pass          *bool              `json:"pass,omitempty"` // noul: p >= threshold
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Norm          *float64           `json:"norm,omitempty"` // score / (levels-1)
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Raw           json.RawMessage    `json:"raw,omitempty"`
}

// JudgeResult is the response of POST /v1/judge.
type JudgeResult struct {
	Answers []JudgeAnswer `json:"answers"`
	Stats   *JudgeStats   `json:"stats"`
}

// JudgeStats mirrors wire.Stats (declared here to avoid an import cycle).
type JudgeStats struct {
	CollectRows  int     `json:"collect_rows"`
	Judged       int     `json:"judged"`
	Requests     int     `json:"requests"`
	CacheHits    int     `json:"cache_hits"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	USD          float64 `json:"usd"`
	ElapsedMS    int64   `json:"elapsed_ms"`
}
