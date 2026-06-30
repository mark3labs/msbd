package dashboard

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// ---- Snapshots ----

func (h *Handler) snapshotList(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rows, err := h.snapshotRows(r.Context())
	if notifyErr(sse, "List snapshots", err) {
		return
	}
	_ = sse.PatchElementTempl(views.SnapshotsPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}

func (h *Handler) snapshotRows(ctx context.Context) ([]views.SnapshotRow, error) {
	snaps, err := h.svc.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.SnapshotRow, 0, len(snaps))
	for i := range snaps {
		s := &snaps[i]
		name := "—"
		if s.Name != nil {
			name = *s.Name
		}
		size := "—"
		if s.SizeBytes != nil {
			size = views.HumanBytes(*s.SizeBytes)
		}
		rows = append(rows, views.SnapshotRow{
			Digest:    s.Digest,
			Name:      name,
			ImageRef:  s.ImageRef,
			Format:    s.Format,
			Size:      size,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
		})
	}
	return rows, nil
}

type createSnapSignals struct {
	Source string `json:"snapsource"`
	Name   string `json:"snapname"`
}

func (h *Handler) snapshotCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createSnapSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	src := strings.TrimSpace(sig.Source)
	if src == "" {
		notify(sse, toast.VariantWarning, "Create snapshot", "source sandbox is required")
		return
	}
	_, err := h.svc.CreateSnapshot(r.Context(), core.SnapshotCreateParams{
		SourceSandbox: src,
		Name:          strings.TrimSpace(sig.Name),
	})
	if notifyErr(sse, "Create snapshot", err) {
		return
	}
	closeDialog(sse, "create-snapshot")
	notify(sse, toast.VariantSuccess, "Created", src)
	h.reRenderSnapshots(r.Context(), sse)
}

func (h *Handler) snapshotDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Delete snapshot", h.svc.RemoveSnapshot(r.Context(), name, true)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Deleted", views.ShortDigest(name))
	h.reRenderSnapshots(r.Context(), sse)
}

func (h *Handler) reRenderSnapshots(ctx context.Context, sse *datastar.ServerSentEventGenerator) {
	rows, err := h.snapshotRows(ctx)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.SnapshotsPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}
