package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// prettyJSON re-indents a JSON string for display; returns the input on error.
func prettyJSON(s string) string {
	if s == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// parseEnv turns "KEY=VALUE" lines into a map.
func parseEnv(s string) map[string]string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// fmtDuration renders an uptime (seconds) compactly: 1d2h, 3h4m, 5m6s, 7s.
func fmtDuration(secs float64) string {
	d := time.Duration(secs) * time.Second
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
}

func maxZero(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}
