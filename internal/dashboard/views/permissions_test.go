package views

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func TestAdminContext(t *testing.T) {
	ctx := context.Background()
	if !CanAdmin(ctx) || !CanAdmin(WithAdmin(ctx, true)) {
		t.Fatal("standalone and admin rendering must remain unrestricted")
	}
	viewer := WithAdmin(ctx, false)
	if CanAdmin(viewer) || !CanAdmin(WithAdmin(viewer, true)) || !CanAdmin(ctx) {
		t.Fatal("permissions must be explicit, overridable, and scoped to context")
	}
}

func TestViewerMutationVisibility(t *testing.T) {
	m := testMeta()
	s := TableSort{Col: "name", Dir: "asc"}
	sbx := []SandboxRow{
		{ID: "sbx_running", Image: "alpine:3", State: "running", Workdir: "/"},
		{ID: "sbx_stopped", Image: "alpine:3", State: "stopped", Workdir: "/"},
	}
	vol := []VolumeRow{{Name: "data", Kind: "dir", Path: "/data", Used: "1 MiB"}}
	img := []ImageRow{{Reference: "alpine:3", OS: "linux", Architecture: "amd64", Size: "5 MiB"}}
	snap := []SnapshotRow{{Name: "backup", Digest: "sha256:abcdef", ImageRef: "alpine:3"}}
	cases := []struct {
		name      string
		view      templ.Component
		mutations []string
		reads     []string
	}{
		{"overview", OverviewPage(m, OverviewData{Recent: sbx}), []string{"New sandbox", "create-sandbox"}, []string{"Refresh", "sbx_running", "/sandboxes/sbx_running"}},
		{"overview empty", OverviewPage(m, OverviewData{}), []string{"New sandbox", "create-sandbox"}, []string{"Refresh", "No sandboxes yet"}},
		{"sandboxes", SandboxesPage(m, sbx, s), []string{"New sandbox", "create-sandbox", "create-snapshot", "/stop", "/start", "@delete", "/terminal/"}, []string{"Refresh", "Filter sandboxes", "/sandboxes/sbx_running", "sbx_stopped"}},
		{"sandboxes empty", SandboxesPage(m, nil, s), []string{"New sandbox", "create-sandbox", "create-snapshot"}, []string{"No sandboxes yet", "Refresh"}},
		{"sandbox fragment", SandboxTable(sbx, s), []string{"@post", "@delete", "/terminal/"}, []string{"sbx_running", "sbx_stopped"}},
		{"volumes", VolumesPage(vol, s), []string{"New volume", "create-volume", "@delete"}, []string{"Refresh", "data", "/data"}},
		{"volumes empty", VolumesPage(nil, s), []string{"New volume", "create-volume"}, []string{"No volumes", "Refresh"}},
		{"volume fragment", VolumeTable(vol, s), []string{"@delete"}, []string{"data"}},
		{"images", ImagesPage(m, img, s), []string{"Pull image", "pull-image", "create-sandbox", "Re-pull alpine", "Prune unused images", "@post", "@delete"}, []string{"Refresh", "Inspect alpine:3", "/ui/images/inspect"}},
		{"images empty", ImagesPage(m, nil, s), []string{"Pull image", "pull-image", "create-sandbox", "Prune unused images"}, []string{"No cached images", "Refresh"}},
		{"image fragment", ImageTable(img, s), []string{"@post", "@delete", "create-sandbox"}, []string{"Inspect alpine:3"}},
		{"image inspection", ImageDetailDialog(ImageDetailView{ImageRow: img[0]}), []string{"Launch sandbox", "create-sandbox"}, []string{"alpine:3", "Close", "Digest"}},
		{"snapshots", SnapshotsPage(snap, sbx, s), []string{"New snapshot", "create-snapshot", "@post", "@delete"}, []string{"Refresh", "backup"}},
		{"snapshots empty", SnapshotsPage(nil, sbx, s), []string{"New snapshot", "create-snapshot"}, []string{"No snapshots", "Refresh"}},
		{"snapshot fragment", SnapshotTable(snap, s), []string{"@post", "@delete"}, []string{"backup"}},
		{"detail", SandboxDetailPage(SandboxDetail{SandboxRow: sbx[0], Config: "raw config"}, sbx), []string{"create-snapshot", "/start", "/stop", "@delete", "/terminal/", "/run", "/files/upload", "New folder", "new-folder", "file-upload"}, []string{"Live metrics", "/metrics", "Reload logs", "Download logs", "Refresh listing", "/files", "raw config"}},
		{"files fragment", FilesPanel("sbx_running", "/", nil, []FileRow{{Name: "hello.txt", Path: "/hello.txt"}, {Name: "folder", Path: "/folder", IsDir: true}}), []string{"@delete"}, []string{"hello.txt", "/files/view", "/files/download", "folder/", "Go to path"}},
		{"file viewer", FileViewContent("sbx_running", "/hello.txt", "hello world", true, ""), []string{"Save", "/files/save", "textarea"}, []string{"Close", "/hello.txt"}},
		{"running job", RunBlock(JobView{SandboxID: "sbx_running", JobID: "job", Cmd: "echo hello", Stdout: "hello"}), []string{"Cancel this command", "/cancel"}, []string{"echo hello", "hello"}},
		{"confirmation", ConfirmDialog(), []string{"id=", "Confirm"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			renderHTML := func(ctx context.Context) string {
				t.Helper()
				var b bytes.Buffer
				if err := tc.view.Render(ctx, &b); err != nil {
					t.Fatal(err)
				}
				return b.String()
			}
			viewer := renderHTML(WithAdmin(t.Context(), false))
			admin := renderHTML(WithAdmin(t.Context(), true))
			standalone := renderHTML(t.Context())
			for _, marker := range tc.mutations {
				if strings.Contains(viewer, marker) {
					t.Errorf("viewer sees mutation %q", marker)
				}
				if !strings.Contains(admin, marker) {
					t.Errorf("admin lost mutation %q", marker)
				}
				if !strings.Contains(standalone, marker) {
					t.Errorf("standalone lost mutation %q", marker)
				}
			}
			for _, marker := range tc.reads {
				if !strings.Contains(viewer, marker) {
					t.Errorf("viewer lost read control/data %q", marker)
				}
			}
			if tc.name == "file viewer" && !strings.Contains(viewer, "hello world") {
				t.Error("viewer must see file contents rather than an editable textarea")
			}
		})
	}
}
