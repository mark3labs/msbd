package views

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/a-h/templ"
)

func render(t *testing.T, name string, c templ.Component) {
	t.Helper()
	if err := c.Render(context.Background(), io.Discard); err != nil {
		t.Errorf("%s render: %v", name, err)
	}
}

func testMeta() Meta {
	return Meta{
		Version:        "test",
		DefaultImage:   "microsandbox/python",
		RuntimeVersion: "1.0",
		SDKVersion:     "0.6.7",
		Section:        SectionSandboxes,
		Title:          "Sandboxes",
		Images:         []string{"alpine:3", "microsandbox/python"},
		Volumes:        []string{"data"},
	}
}

func TestViewsRender(t *testing.T) {
	m := testMeta()
	now := time.Now()
	sort := TableSort{Col: "id", Dir: "asc"}
	sbx := []SandboxRow{{ID: "sbx_1", Image: "alpine", State: "running", Workdir: "/", Uptime: "5s", CreatedAt: now}}
	det := SandboxDetail{SandboxRow: sbx[0], Config: `{"a":1}`}

	render(t, "Page", Page(m, OverviewPage(m, OverviewData{})))
	render(t, "OverviewPage", OverviewPage(m, OverviewData{Total: 2, Running: 1, MaxSandboxes: 4, CapPercent: 25, MetricsOK: true, Recent: sbx}))
	render(t, "OverviewBodyEmpty", OverviewBody(OverviewData{}))
	render(t, "SandboxesPage", SandboxesPage(m, sbx, sort))
	render(t, "SandboxesPageEmpty", SandboxesPage(m, nil, sort))
	render(t, "SandboxTable", SandboxTable(sbx, sort))
	render(t, "SandboxDetailPage", SandboxDetailPage(det, sbx))
	render(t, "RunBlockRunning", RunBlock(JobView{SandboxID: "sbx_1", JobID: "j1", Cmd: "ls", State: "running", Elapsed: "1s"}))
	render(t, "RunBlockDone", RunBlock(JobView{SandboxID: "sbx_1", JobID: "j1", Cmd: "ls", State: "exited", Done: true, Stdout: "ok", Elapsed: "1s"}))
	render(t, "RunBlockFailed", RunBlock(JobView{SandboxID: "sbx_1", JobID: "j1", Cmd: "boom", State: "exited", Done: true, ExitCode: 2, Stderr: "boom", Truncated: true}))
	render(t, "MetricsPanel", MetricsPanel("sbx_1"))
	render(t, "LogsPanel", LogsPanel([]LogLine{{Source: "stdout", Text: "hello", Timestamp: now}}))
	render(t, "LogsPanelEmpty", LogsPanel(nil))
	render(t, "FilesPanel", FilesPanel("sbx_1", "/x", crumbs(), []FileRow{{Name: "a", Path: "/x/a", Kind: "directory", IsDir: true, Size: "0 B", Mode: "0755"}}))
	render(t, "FilesPanelEmpty", FilesPanel("sbx_1", "/", crumbs(), nil))
	render(t, "FileViewContent", FileViewContent("sbx_1", "/etc/hosts", "127.0.0.1", true, ""))
	render(t, "FileViewBinary", FileViewContent("sbx_1", "/bin/ls", "0000", false, "binary"))
	render(t, "VolumesPage", VolumesPage([]VolumeRow{{Name: "v1", Kind: "dir", Path: "/x", Used: "0 B", Capacity: "—", CreatedAt: now}}, sort))
	render(t, "VolumesPageEmpty", VolumesPage(nil, sort))
	render(t, "ImagesPage", ImagesPage(m, []ImageRow{{Reference: "alpine:3", Architecture: "arm64", OS: "linux", Layers: 2, Size: "5 MiB", LastUsedAt: now, InUse: true}}, sort))
	render(t, "ImagesPageEmpty", ImagesPage(m, nil, sort))
	render(t, "ImageDetailDialog", ImageDetailDialog(ImageDetailView{
		ImageRow:  ImageRow{Reference: "alpine:3", Digest: "sha256:abc", Size: "5 MiB"},
		Env:       []string{"PATH=/bin"},
		LayerRows: []ImageLayerRow{{Digest: "sha256:def", Size: "1 MiB"}},
	}))
	render(t, "SnapshotsPage", SnapshotsPage([]SnapshotRow{{Digest: "sha256:deadbeef", Name: "snap", ImageRef: "alpine", Format: "vmdk", Size: "1 MiB", CreatedAt: now}}, sbx, sort))
	render(t, "SnapshotsPageEmpty", SnapshotsPage(nil, nil, sort))
	render(t, "TerminalPage", TerminalPage("sbx_1", "ws://localhost/v1/sandboxes/sbx_1/terminal", "tok", false))
	render(t, "TerminalPageEmbed", TerminalPage("sbx_1", "ws://localhost/v1/sandboxes/sbx_1/terminal", "tok", true))
	render(t, "TerminalNotFound", TerminalNotFound("sbx_1"))
	render(t, "CreateSandboxDialog", CreateSandboxDialog(m))
	render(t, "CreateVolumeDialog", CreateVolumeDialog())
	render(t, "PullImageDialog", PullImageDialog(m))
	render(t, "CreateSnapshotDialog", CreateSnapshotDialog(sbx, "sbx_1"))
	render(t, "ConfirmDialog", ConfirmDialog())
	render(t, "InlineError", InlineError("slot", "Boom", "it broke"))
	render(t, "EmptyState", EmptyState("server", "Nothing", "here"))
	render(t, "TableSkeleton", TableSkeleton(3))

	keys := []KeyRow{{ID: "1", Name: "ci", Prefix: "msbd_abcd1234", Status: "active", Active: true, CreatedAt: now}}
	users := []UserRow{{Username: "alice", Role: "admin", CreatedAt: now, Self: true, Protected: true}}
	render(t, "KeysPage", KeysPage(keys, sort))
	render(t, "KeysPageEmpty", KeysPage(nil, sort))
	render(t, "KeyTableRevoked", KeyTable([]KeyRow{{ID: "2", Name: "old", Status: "revoked"}}, sort))
	render(t, "UsersPage", UsersPage(users, sort))
	render(t, "UsersPageEmpty", UsersPage(nil, sort))
	render(t, "UserTableViewer", UserTable([]UserRow{{Username: "bob", Role: "viewer"}}, sort))
	render(t, "CreateKeyDialog", CreateKeyDialog())
	render(t, "NewKeyDialog", NewKeyDialog("ci", "msbd_secret"))
	render(t, "CreateUserDialog", CreateUserDialog())
	render(t, "SetPasswordDialog", SetPasswordDialog())
	render(t, "ChangePasswordDialog", ChangePasswordDialog())
	render(t, "LoginPage", LoginPage("/dashboard", "test"))
	render(t, "LoginError", LoginError("incorrect username or password"))
	render(t, "LockedPage", LockedPage("test"))
}

func crumbs() []Crumb {
	return []Crumb{{Label: "/", Path: "/"}, {Label: "x", Path: "/x"}}
}

// TestNavMarksActiveSection guards the "which page am I on?" affordance.
func TestNavMarksActiveSection(t *testing.T) {
	for _, tc := range []struct {
		sec  Section
		href string
	}{
		{SectionOverview, "/dashboard"},
		{SectionSandboxes, "/dashboard/sandboxes"},
		{SectionVolumes, "/dashboard/volumes"},
		{SectionImages, "/dashboard/images"},
		{SectionSnapshots, "/dashboard/snapshots"},
	} {
		m := testMeta()
		m.Section = tc.sec
		var b strings.Builder
		if err := Page(m, EmptyState("box", "x", "y")).Render(context.Background(), &b); err != nil {
			t.Fatal(err)
		}
		out := b.String()
		if !strings.Contains(out, `href="`+tc.href+`"`) {
			t.Errorf("%s: nav link %q missing", tc.sec, tc.href)
		}
		if !strings.Contains(out, `aria-current="page"`) {
			t.Errorf("%s: no active nav item marked", tc.sec)
		}
	}
}

// TestPageTitle keeps per-section document titles working (browser tabs and
// history entries are useless without them).
func TestPageTitle(t *testing.T) {
	m := testMeta()
	m.Title = "Volumes"
	var b strings.Builder
	_ = Page(m, EmptyState("box", "x", "y")).Render(context.Background(), &b)
	if !strings.Contains(b.String(), "<title>Volumes · msbd</title>") {
		t.Error("per-page <title> missing")
	}
}

// TestNoNativeConfirm ensures destructive actions use the styled confirm dialog
// rather than window.confirm().
func TestNoNativeConfirm(t *testing.T) {
	sort := TableSort{Col: "id", Dir: "asc"}
	m := testMeta()
	pages := map[string]templ.Component{
		"sandboxes": SandboxesPage(m, []SandboxRow{{ID: "sbx_1", State: "running"}}, sort),
		"volumes":   VolumesPage([]VolumeRow{{Name: "v"}}, sort),
		"images":    ImagesPage(m, []ImageRow{{Reference: "alpine"}}, sort),
		"snapshots": SnapshotsPage([]SnapshotRow{{Digest: "sha256:a", Name: "s"}}, nil, sort),
		"keys":      KeysPage([]KeyRow{{ID: "1", Name: "ci", Status: "active", Active: true}}, sort),
		"users":     UsersPage([]UserRow{{Username: "bob", Role: "viewer"}}, sort),
	}
	for name, c := range pages {
		var b strings.Builder
		_ = c.Render(context.Background(), &b)
		if strings.Contains(b.String(), "confirm(") {
			t.Errorf("%s still uses window.confirm()", name)
		}
		if !strings.Contains(b.String(), "__msbdConfirmRun") {
			t.Errorf("%s does not arm the confirm dialog", name)
		}
	}
}

// TestIconButtonsHaveAccessibleNames guards against icon-only controls that a
// screen reader announces as "button".
func TestIconButtonsHaveAccessibleNames(t *testing.T) {
	var b strings.Builder
	_ = SandboxTable([]SandboxRow{{ID: "sbx_1", State: "running"}}, TableSort{}).Render(context.Background(), &b)
	out := b.String()
	for _, want := range []string{
		`aria-label="Stop sbx_1"`,
		`aria-label="Open a terminal in sbx_1"`,
		`aria-label="Delete sbx_1"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing accessible name: %s", want)
		}
	}
}

// TestTablesScrollHorizontally guards the mobile clipping fix.
func TestTablesScrollHorizontally(t *testing.T) {
	var b strings.Builder
	_ = SandboxTable([]SandboxRow{{ID: "sbx_1"}}, TableSort{}).Render(context.Background(), &b)
	if !strings.Contains(b.String(), "overflow-x-auto") {
		t.Error("table is not horizontally scrollable")
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[uint64]string{0: "0 B", 512: "512 B", 1024: "1.0 KiB", 1536: "1.5 KiB", 1 << 30: "1.0 GiB"}
	for in, want := range cases {
		if got := HumanBytes(in); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestRelTime(t *testing.T) {
	if got := RelTime(time.Time{}); got != "—" {
		t.Errorf("zero time = %q, want em dash", got)
	}
	if got := RelTime(time.Now().Add(-90 * time.Minute)); got != "1h ago" {
		t.Errorf("90m ago = %q, want 1h ago", got)
	}
	if got := RelTime(time.Now().Add(-3 * time.Second)); got != "just now" {
		t.Errorf("3s ago = %q, want just now", got)
	}
}

func TestTableSortToggles(t *testing.T) {
	s := TableSort{Col: "name", Dir: "asc"}
	if s.NextDir("name") != "desc" {
		t.Error("clicking the active ascending column should flip to desc")
	}
	if s.NextDir("size") != "asc" {
		t.Error("a new column should start ascending")
	}
	if s.Arrow("name") != "↑" || s.Arrow("size") != "" {
		t.Error("sort arrow rendered on the wrong column")
	}
}

// TestMutatingActionsDisableRetry guards against duplicate side effects:
// Datastar retries dropped fetches by default, so a POST that boots a sandbox
// or runs a command must opt out. A new call site that forgets fails here.
func TestMutatingActionsDisableRetry(t *testing.T) {
	m := testMeta()
	sort := TableSort{Col: "id", Dir: "asc"}
	sbx := []SandboxRow{{ID: "sbx_1", Image: "alpine", State: "running", Workdir: "/"}}

	pages := map[string]templ.Component{
		"sandboxes":       SandboxesPage(m, sbx, sort),
		"detail":          SandboxDetailPage(SandboxDetail{SandboxRow: sbx[0], Config: "{}"}, sbx),
		"volumes":         VolumesPage([]VolumeRow{{Name: "v"}}, sort),
		"images":          ImagesPage(m, []ImageRow{{Reference: "alpine"}}, sort),
		"snapshots":       SnapshotsPage([]SnapshotRow{{Digest: "sha256:a", Name: "s"}}, sbx, sort),
		"create-sandbox":  CreateSandboxDialog(m),
		"create-volume":   CreateVolumeDialog(),
		"pull-image":      PullImageDialog(m),
		"create-snapshot": CreateSnapshotDialog(sbx, ""),
		"files":           FilesPanel("sbx_1", "/", crumbs(), []FileRow{{Name: "a", Path: "/a"}}),
		"run-block":       RunBlock(JobView{SandboxID: "sbx_1", JobID: "j1", Cmd: "ls"}),
		"keys":            KeysPage([]KeyRow{{ID: "1", Name: "ci", Status: "active", Active: true}}, sort),
		"users":           UsersPage([]UserRow{{Username: "bob", Role: "viewer"}}, sort),
		"create-key":      CreateKeyDialog(),
		"create-user":     CreateUserDialog(),
		"set-password":    SetPasswordDialog(),
		"change-password": ChangePasswordDialog(),
		"login":           LoginPage("/dashboard", "test"),
	}
	for name, c := range pages {
		var b strings.Builder
		if err := c.Render(context.Background(), &b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := b.String()
		for _, verb := range []string{"@post(", "@delete("} {
			for i := 0; ; {
				idx := strings.Index(out[i:], verb)
				if idx < 0 {
					break
				}
				at := i + idx
				end := strings.Index(out[at:], ")")
				if end < 0 {
					break
				}
				call := out[at : at+end+1]
				if !strings.Contains(call, "retry:&#39;never&#39;") && !strings.Contains(call, "retry:'never'") {
					t.Errorf("%s: mutating action without retry:'never': %s", name, call)
				}
				i = at + len(verb)
			}
		}
	}
}

// TestIsMutableTag pins which references a force re-pull can actually change.
// Getting this wrong either nags on immutable refs or silently no-ops on the
// one case (:latest) where users expect "pull" to fetch something newer.
func TestIsMutableTag(t *testing.T) {
	mutable := []string{
		"alpine",                       // implicit :latest
		"alpine:latest",                //
		"microsandbox/python",          //
		"ghcr.io/mark3labs/kit:latest", //
		"localhost:5000/repo",          // registry port must not read as a tag
	}
	immutable := []string{
		"alpine:3.19",
		"microsandbox/python:1.2.3",
		"alpine@sha256:abc123",
		"ghcr.io/x/y@sha256:deadbeef",
		"localhost:5000/repo:v1",
	}
	for _, r := range mutable {
		if !IsMutableTag(r) {
			t.Errorf("IsMutableTag(%q) = false, want true", r)
		}
	}
	for _, r := range immutable {
		if IsMutableTag(r) {
			t.Errorf("IsMutableTag(%q) = true, want false", r)
		}
	}
}

// TestImagesPageOffersPullManagement guards the image-management surface: every
// action a user needs must be reachable from the page.
func TestImagesPageOffersPullManagement(t *testing.T) {
	m := testMeta()
	rows := []ImageRow{{Reference: "alpine:latest", Size: "5 MiB", SizeBytes: 5 << 20, Layers: 1}}
	var b strings.Builder
	if err := ImagesPage(m, rows, TableSort{}).Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"/dashboard/api/images/pull",         // pull + re-pull
		"force=1",                            // row re-pull forces
		"/dashboard/api/images/prune",        // prune posts to the real route
		"/dashboard/api/images?ref=",         // remove
		"/dashboard/api/images/inspect?ref=", // inspect
		"moving tag",                         // mutable-tag hint
		"aria-label=\"Remove image alpine:latest\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("images page missing %q", want)
		}
	}
	// The prune button used to point at an unmounted preview route.
	if strings.Contains(out, "prune-preview") {
		t.Error("prune button references a route that is not mounted")
	}
}

func TestPullDialogSuggestsImages(t *testing.T) {
	m := testMeta()
	m.Images = []string{"alpine:3", "microsandbox/python"}
	var b strings.Builder
	_ = PullImageDialog(m).Render(context.Background(), &b)
	out := b.String()
	if !strings.Contains(out, "microsandbox/python") {
		t.Error("pull dialog should suggest first-party sandbox images")
	}
	if !strings.Contains(out, "pull-suggestions") {
		t.Error("pull dialog should offer an autocomplete datalist")
	}
	// Cached images must appear exactly once even though they overlap the
	// starter list.
	if n := strings.Count(out, `<option value="microsandbox/python">`); n != 1 {
		t.Errorf("suggestion list should de-duplicate, got %d entries", n)
	}
}

func TestPullProgressRenders(t *testing.T) {
	var b strings.Builder
	if err := PullProgress("alpine:3", "42s").Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "alpine:3") || !strings.Contains(out, "42s") {
		t.Error("pull progress should show the reference and elapsed time")
	}
	if !strings.Contains(out, `id="pull-progress"`) {
		t.Error("progress must keep the SSE target id so it can be patched/cleared")
	}
}

func TestTotalImageSize(t *testing.T) {
	got := totalImageSize([]ImageRow{{SizeBytes: 1 << 20}, {SizeBytes: 3 << 20}})
	if got != "4.0 MiB" {
		t.Errorf("totalImageSize = %q, want 4.0 MiB", got)
	}
}
