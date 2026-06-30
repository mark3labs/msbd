package views

import (
	"context"
	"io"
	"testing"

	"github.com/a-h/templ"
)

func render(t *testing.T, name string, c templ.Component) {
	t.Helper()
	if err := c.Render(context.Background(), io.Discard); err != nil {
		t.Errorf("%s render: %v", name, err)
	}
}

func TestViewsRender(t *testing.T) {
	meta := Meta{Version: "test", DefaultImage: "microsandbox/python", RuntimeVersion: "1.0", SDKVersion: "0.6.1"}
	sbx := []SandboxRow{{ID: "sbx_1", Image: "alpine", State: "running", Workdir: "/", Uptime: "5s"}}
	det := SandboxDetail{SandboxRow: sbx[0], Config: `{"a":1}`}

	render(t, "Layout", Layout(meta))
	render(t, "SandboxesPage", SandboxesPage(sbx))
	render(t, "SandboxesPageEmpty", SandboxesPage(nil))
	render(t, "SandboxTable", SandboxTable(sbx))
	render(t, "SandboxDetailPage", SandboxDetailPage(det))
	render(t, "RunOutput", RunOutput(0, "ok", ""))
	render(t, "RunOutputErr", RunOutput(1, "", "boom"))
	render(t, "MetricsPanel", MetricsPanel("sbx_1"))
	render(t, "LogsPanel", LogsPanel([]LogLine{{Source: "stdout", Text: "hello"}}))
	render(t, "LogsPanelEmpty", LogsPanel(nil))
	render(t, "FilesPanel", FilesPanel([]FileRow{{Path: "/a", Kind: "dir", Size: "0 B", Mode: "0755"}}))
	render(t, "VolumesPage", VolumesPage([]VolumeRow{{Name: "v1", Kind: "overlay", Path: "/x", Used: "0 B", Capacity: "—"}}))
	render(t, "ImagesPage", ImagesPage([]ImageRow{{Reference: "alpine:3", Architecture: "arm64", OS: "linux", Layers: 2, Size: "5 MiB"}}))
	render(t, "SnapshotsPage", SnapshotsPage([]SnapshotRow{{Digest: "sha256:deadbeef", Name: "snap", ImageRef: "alpine", Format: "vmdk", Size: "1 MiB"}}))
	render(t, "TerminalPage", TerminalPage("sbx_1", "ws://localhost/v1/sandboxes/sbx_1/terminal", "tok"))
	render(t, "CreateSandboxDialog", CreateSandboxDialog("microsandbox/python"))
	render(t, "CreateVolumeDialog", CreateVolumeDialog())
	render(t, "CreateSnapshotDialog", CreateSnapshotDialog())
}
