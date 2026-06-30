package views

import "fmt"

// View models. Handlers map core.* types into these so the templ views stay
// free of any business/SDK types and can carry pre-formatted, display-ready
// fields.

type SandboxRow struct {
	ID      string
	Image   string
	State   string
	Workdir string
	Uptime  string
	Labels  map[string]string
}

type SandboxDetail struct {
	SandboxRow
	Config string // pretty-printed JSON
}

type VolumeRow struct {
	Name      string
	Path      string
	Kind      string
	Used      string
	Capacity  string
	CreatedAt string
}

type ImageRow struct {
	Reference    string
	Architecture string
	OS           string
	Layers       uint
	Size         string
	LastUsedAt   string
}

type SnapshotRow struct {
	Digest    string
	Name      string
	ImageRef  string
	Format    string
	Size      string
	CreatedAt string
}

type LogLine struct {
	Source    string
	Timestamp string
	Text      string
}

type FileRow struct {
	Path string
	Kind string
	Size string
	Mode string
}

// HumanBytes renders a byte count in IEC units.
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// ShortDigest trims a long digest for display.
func ShortDigest(d string) string {
	if len(d) > 19 {
		return d[:19] + "…"
	}
	return d
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// stateBadge maps a sandbox state to a templui badge variant string.
func stateBadge(state string) string {
	switch state {
	case "running":
		return "default"
	case "stopped", "paused":
		return "secondary"
	case "error", "failed":
		return "destructive"
	default:
		return "outline"
	}
}
