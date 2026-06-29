package core

// logs.go — read persisted sandbox logs (stdout/stderr/system).

import (
	"context"
	"fmt"
	"time"

	msb "github.com/superradcompany/microsandbox/sdk/go"
)

// LogEntry is one persisted sandbox log line.
type LogEntry struct {
	Source    string
	Timestamp time.Time
	Text      string
	Cursor    string
}

// LogQuery filters persisted logs.
type LogQuery struct {
	Tail    uint64
	Sources []string // stdout | stderr | output | system
}

// Logs reads persisted logs for a sandbox, applying tail/source filters.
func (s *Service) Logs(ctx context.Context, id string, q LogQuery) ([]LogEntry, error) {
	h, err := msb.GetSandbox(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	opts := msb.LogOptions{Tail: q.Tail}
	for _, src := range q.Sources {
		switch src {
		case "stdout":
			opts.Sources = append(opts.Sources, msb.LogSourceStdout)
		case "stderr":
			opts.Sources = append(opts.Sources, msb.LogSourceStderr)
		case "output":
			opts.Sources = append(opts.Sources, msb.LogSourceOutput)
		case "system":
			opts.Sources = append(opts.Sources, msb.LogSourceSystem)
		}
	}
	entries, err := h.Logs(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("logs %s: %w", id, err)
	}
	out := make([]LogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, LogEntry{
			Source:    string(e.Source),
			Timestamp: e.Timestamp,
			Text:      e.Text(),
			Cursor:    e.Cursor,
		})
	}
	return out, nil
}
