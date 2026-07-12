package autopilot

import (
	"encoding/json"
	"os"
	"time"
)

type decisionRecord struct {
	At           time.Time      `json:"at"`
	Request      string         `json:"request"`
	Query        string         `json:"query"`
	Resolution   string         `json:"resolution,omitempty"`
	Season       string         `json:"season,omitempty"`
	MinSeeders   int            `json:"min_seeders"`
	MaxDownloads int            `json:"max_downloads"`
	MaxSize      int64          `json:"max_size_bytes,omitempty"`
	Categories   []string       `json:"categories,omitempty"`
	Picks        []decisionPick `json:"picks,omitempty"`
	Rejected     map[string]int `json:"rejected,omitempty"`
	TotalBytes   int64          `json:"total_bytes,omitempty"`
	Queued       int            `json:"queued"`
	Outcome      string         `json:"outcome"`
}

type decisionPick struct {
	Title    string  `json:"title"`
	Provider string  `json:"provider"`
	Size     int64   `json:"size_bytes,omitempty"`
	Seeders  int     `json:"seeders"`
	Score    float64 `json:"score"`
	Reason   string  `json:"reason"`
}

func appendDecision(path, raw string, in Intent, plan Plan) error {
	rec := decisionRecord{
		At:           time.Now().UTC(),
		Request:      raw,
		Query:        in.Query,
		Resolution:   resStr(in),
		Season:       seasonStr(in),
		MinSeeders:   in.MinSeeders,
		MaxDownloads: in.Max,
		MaxSize:      in.MaxSizeBytes,
		Categories:   append([]string(nil), in.Categories...),
		Rejected:     plan.Rejected,
		TotalBytes:   plan.TotalBytes,
		Queued:       plan.Queued,
		Outcome:      plan.Outcome,
	}
	for _, p := range plan.Picks {
		rec.Picks = append(rec.Picks, decisionPick{
			Title: p.Result.Title, Provider: p.Result.Provider,
			Size: p.Result.SizeBytes, Seeders: p.Result.Seeders,
			Score: p.Score, Reason: p.Reason,
		})
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(rec)
}
