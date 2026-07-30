package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/msbd/internal/core"
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

// parseSecrets turns "KEY=VALUE" lines into injected secret params.
func parseSecrets(s string) []core.SecretParam {
	m := parseEnv(s)
	if len(m) == 0 {
		return nil
	}
	out := make([]core.SecretParam, 0, len(m))
	for k, v := range m {
		out = append(out, core.SecretParam{EnvVar: k, Value: v})
	}
	return out
}

// parsePorts turns "host:guest" / "host:guest/proto" lines into port mappings.
func parsePorts(s string) ([]core.PortMapping, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []core.PortMapping
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		spec, proto, hasProto := strings.Cut(line, "/")
		if !hasProto {
			proto = "tcp"
		}
		proto = strings.ToLower(strings.TrimSpace(proto))
		if proto != "tcp" && proto != "udp" {
			return nil, fmt.Errorf("%q: protocol must be tcp or udp", line)
		}
		hostStr, guestStr, ok := strings.Cut(spec, ":")
		if !ok {
			return nil, fmt.Errorf("%q: expected host:guest", line)
		}
		host, err := parsePort(hostStr)
		if err != nil {
			return nil, fmt.Errorf("%q: host %w", line, err)
		}
		guest, err := parsePort(guestStr)
		if err != nil {
			return nil, fmt.Errorf("%q: guest %w", line, err)
		}
		out = append(out, core.PortMapping{HostPort: host, GuestPort: guest, Protocol: proto})
	}
	return out, nil
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("port %q is not a number", strings.TrimSpace(s))
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port %d out of range (1-65535)", n)
	}
	return n, nil
}

// parseMounts turns "volume:/guest/path" lines (optionally ":ro") into mounts.
func parseMounts(s string) ([]core.MountParam, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var out []core.MountParam
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 2 {
			return nil, fmt.Errorf("%q: expected volume:/guest/path", line)
		}
		vol := strings.TrimSpace(parts[0])
		guest := strings.TrimSpace(parts[1])
		if vol == "" || !strings.HasPrefix(guest, "/") {
			return nil, fmt.Errorf("%q: expected volume:/absolute/guest/path", line)
		}
		m := core.MountParam{Volume: vol, GuestPath: guest}
		if len(parts) > 2 && strings.EqualFold(strings.TrimSpace(parts[2]), "ro") {
			m.Readonly = true
		}
		out = append(out, m)
	}
	return out, nil
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

// safeFilename strips anything that could break a Content-Disposition header.
func safeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '"' || r == '\\' || r == '\n' || r == '\r' || r < 0x20:
			return -1
		default:
			return r
		}
	}, s)
	if s == "" {
		return "download"
	}
	return s
}

// plural renders "1 layer" / "3 layers".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
