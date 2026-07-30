package views

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// View models. Handlers map core.* types into these so the templ views stay
// free of any business/SDK types and can carry pre-formatted, display-ready
// fields.

// Section identifies the active top-level nav entry so the shell can highlight
// it and set a matching document title.
type Section string

const (
	SectionOverview  Section = "overview"
	SectionSandboxes Section = "sandboxes"
	SectionVolumes   Section = "volumes"
	SectionImages    Section = "images"
	SectionSnapshots Section = "snapshots"
)

type SandboxRow struct {
	ID        string
	Image     string
	State     string
	Workdir   string
	Uptime    string
	Labels    map[string]string
	CreatedAt time.Time
}

// Haystack is the lowercase blob the client-side filter matches against.
func (r SandboxRow) Haystack() string {
	return strings.ToLower(r.ID + " " + r.Image + " " + r.State + " " + r.Workdir)
}

type SandboxDetail struct {
	SandboxRow
	Config string // pretty-printed JSON
}

type VolumeRow struct {
	Name          string
	Path          string
	Kind          string
	Used          string
	Capacity      string
	UsedPercent   int // -1 when capacity is unknown
	CreatedAt     time.Time
	Labels        map[string]string
	MountedBy     []string
	MountedByText string
}

func (r VolumeRow) Haystack() string {
	return strings.ToLower(r.Name + " " + r.Kind + " " + r.Path)
}

type ImageRow struct {
	Reference    string
	Digest       string
	Architecture string
	OS           string
	Layers       uint
	Size         string // display string
	SizeBytes    uint64 // raw, so the page can total the cache
	CreatedAt    time.Time
	LastUsedAt   time.Time
	InUse        bool
}

func (r ImageRow) Haystack() string {
	return strings.ToLower(r.Reference + " " + r.Architecture + " " + r.OS)
}

// ImageDetailView is the inspect payload for one cached image.
type ImageDetailView struct {
	ImageRow
	Entrypoint string
	Cmd        string
	WorkingDir string
	User       string
	Env        []string
	LayerRows  []ImageLayerRow
}

type ImageLayerRow struct {
	Digest string
	Size   string
	Media  string
}

type SnapshotRow struct {
	Digest    string
	Name      string
	ImageRef  string
	Format    string
	Size      string
	Path      string
	Parent    string
	CreatedAt time.Time
}

func (r SnapshotRow) Haystack() string {
	return strings.ToLower(r.Name + " " + r.Digest + " " + r.ImageRef + " " + r.Format)
}

type LogLine struct {
	Source    string
	Timestamp time.Time
	Text      string
}

type FileRow struct {
	Name   string
	Path   string
	Kind   string // dir | file | link
	Size   string
	Mode   string
	IsDir  bool
	Hidden bool
}

// Crumb is one breadcrumb segment in the file browser.
type Crumb struct {
	Label string
	Path  string
}

// JobView is a run-command job rendered in the sandbox detail Run panel.
type JobView struct {
	SandboxID string
	JobID     string
	Cmd       string
	State     string // running | exited | failed | gone
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
	Started   time.Time
	Elapsed   string
	Done      bool
	// Cancelled marks a job the user killed from the UI. The runtime reports a
	// SIGKILLed process as exit 0, so without this a cancelled command would
	// render as a successful one.
	Cancelled bool
}

// OverviewData powers the landing page.
type OverviewData struct {
	Total        int
	Running      int
	Stopped      int
	Failed       int
	MaxSandboxes int // 0 = unlimited
	CapPercent   int
	ActiveJobs   int

	CPUPercent  float64
	MemUsed     string
	MemLimit    string
	MemPercent  int
	MetricsOK   bool
	MetricsNote string

	Volumes      int
	VolumesUsed  string
	Images       int
	ImagesSize   string
	Snapshots    int
	SnapshotSize string

	Recent []SandboxRow
}

// TableSort carries the active sort column/direction for a sortable table.
type TableSort struct {
	Col string
	Dir string // "asc" | "desc"
}

// NextDir returns the direction a header link for col should request: clicking
// the active column flips it, any other column starts ascending.
func (t TableSort) NextDir(col string) string {
	if t.Col == col && t.Dir == "asc" {
		return "desc"
	}
	return "asc"
}

// Arrow renders the sort indicator for a column header.
func (t TableSort) Arrow(col string) string {
	if t.Col != col {
		return ""
	}
	if t.Dir == "desc" {
		return "↓"
	}
	return "↑"
}

// Query builds the ?sort=&dir= suffix a header link should request for col.
func (t TableSort) Query(col string) string {
	return "?sort=" + url.QueryEscape(col) + "&dir=" + t.NextDir(col)
}

// ---------------------------------------------------------------------------
// formatting helpers
// ---------------------------------------------------------------------------

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

// RelTime renders a coarse "time ago" string. Zero times render as an em dash.
func RelTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		return "just now"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

// AbsTime is the full timestamp shown in a tooltip next to a relative time.
func AbsTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("2006-01-02 15:04:05 MST")
}

// LogStamp is the compact per-line timestamp in the logs panel.
func LogStamp(t time.Time) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.Format("15:04:05")
}

func itoa(n int) string { return strconv.Itoa(n) }

// pct clamps a percentage into 0..100.
func pct(v float64) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return int(v + 0.5)
	}
}

// pct1 renders a percentage with one decimal.
func pct1(v float64) string { return fmt.Sprintf("%.1f%%", v) }

// jsExpr returns s as a JSON string literal safe to embed INSIDE a Datastar /
// JavaScript expression attribute. templ HTML-escapes attribute values, but the
// browser decodes entities before Datastar evaluates the attribute as JS, so a
// raw ' or } in user/guest-controlled data would otherwise break out and
// execute. JSON escaping is also valid JS-string escaping, closing that hole.
func jsExpr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// pathSeg escapes a value for safe use as a URL path segment.
func pathSeg(s string) string { return url.PathEscape(s) }

// queryVal escapes a value for safe use as a URL query value.
func queryVal(s string) string { return url.QueryEscape(s) }

// stateBadge maps a sandbox state to a templui badge variant string.
func stateBadge(state string) string {
	switch state {
	case "running":
		return "default"
	case "stopped", "paused":
		return "secondary"
	case "error", "failed", "crashed":
		return "destructive"
	default:
		return "outline"
	}
}

// stateDot is the colour of the small status dot rendered next to a state.
func stateDot(state string) string {
	switch state {
	case "running":
		return "bg-emerald-500"
	case "stopped", "paused":
		return "bg-zinc-400"
	case "error", "failed", "crashed":
		return "bg-red-500"
	default:
		return "bg-amber-500"
	}
}

// orDashS renders an em dash for empty display values.
func orDashS(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// lowerOf is the lowercase haystack used by client-side text filters.
func lowerOf(s string) string { return strings.ToLower(s) }

// joinLines joins values with newlines for a <pre> block.
func joinLines(v []string) string { return strings.Join(v, "\n") }

// joinMax joins up to n values, appending an ellipsis when truncated.
func joinMax(v []string, n int) string {
	if len(v) <= n {
		return strings.Join(v, ", ")
	}
	return strings.Join(v[:n], ", ") + ", …"
}

// openNativeJS / closeNativeJS drive a plain <dialog> element (used for the
// dynamically patched file dialogs, which are not templui dialog roots).
func openNativeJS(id string) string {
	return "document.getElementById(" + jsExpr(id) + ")?.showModal();"
}

func closeNativeJS(id string) string {
	return "document.getElementById(" + jsExpr(id) + ")?.close();"
}

// ---------------------------------------------------------------------------
// Datastar action helpers
// ---------------------------------------------------------------------------
//
// Mutating actions MUST disable Datastar's automatic retry. The default retries
// a dropped fetch, and these endpoints are NOT idempotent — a retried POST would
// boot a second sandbox or re-run a command, and a retried DELETE would fire a
// second (now 404-ing) removal. Reads (@get) stay retryable.

const noRetry = ", {retry:'never'}"

// dsPost is a non-idempotent POST action.
func dsPost(url string) string { return "@post(" + jsExpr(url) + noRetry + ")" }

// dsDelete is a non-idempotent DELETE action.
func dsDelete(url string) string { return "@delete(" + jsExpr(url) + noRetry + ")" }

// dsGet is a read; retrying it is harmless, so it keeps the default policy.
func dsGet(url string) string { return "@get(" + jsExpr(url) + ")" }
