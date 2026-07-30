package dashboard

import (
	"context"
	"net/http"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// ---- Snapshots ----

func (h *Handler) snapshotTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	s := parseSort(r, "created")
	rows, err := h.snapshotRows(r.Context(), s)
	if notifyErr(sse, "List snapshots", err) {
		return
	}
	_ = sse.PatchElementTempl(views.SnapshotTable(rows, s))
}

func (h *Handler) snapshotRows(ctx context.Context, s views.TableSort) ([]views.SnapshotRow, error) {
	snaps, err := h.svc.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.SnapshotRow, 0, len(snaps))
	for i := range snaps {
		sn := &snaps[i]
		name := "—"
		if sn.Name != nil && *sn.Name != "" {
			name = *sn.Name
		}
		size := "—"
		if sn.SizeBytes != nil {
			size = views.HumanBytes(*sn.SizeBytes)
		}
		parent := ""
		if sn.ParentDigest != nil {
			parent = *sn.ParentDigest
		}
		rows = append(rows, views.SnapshotRow{
			Digest:    sn.Digest,
			Name:      name,
			ImageRef:  sn.ImageRef,
			Format:    sn.Format,
			Size:      size,
			Path:      sn.Path,
			Parent:    parent,
			CreatedAt: sn.CreatedAt,
		})
	}
	sortRows(rows, s, func(a, b views.SnapshotRow) bool {
		switch s.Col {
		case "name":
			return a.Name < b.Name
		case "size":
			return a.Size < b.Size
		default:
			// Newest first feels right for a snapshot log.
			return b.CreatedAt.Before(a.CreatedAt)
		}
	})
	return rows, nil
}

type createSnapSignals struct {
	Source string `json:"snapsource"`
	Name   string `json:"snapname"`
	Force  bool   `json:"snapforce"`
}

func (h *Handler) snapshotCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createSnapSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	src := strings.TrimSpace(sig.Source)
	if src == "" {
		_ = sse.PatchElementTempl(views.InlineError("create-snapshot-error", "Source required", "Pick the sandbox to snapshot."))
		return
	}
	_, err := h.svc.CreateSnapshot(r.Context(), core.SnapshotCreateParams{
		SourceSandbox: src,
		Name:          strings.TrimSpace(sig.Name),
		Force:         sig.Force,
	})
	if failInline(sse, "create-snapshot-error", "Create snapshot", err) {
		return
	}
	closeDialog(sse, "create-snapshot")
	notify(sse, toast.VariantSuccess, "Snapshot created", src)
	h.reRenderSnapshots(r, sse)
}

// snapshotVerify runs an integrity check and reports the digest it computed.
func (h *Handler) snapshotVerify(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sse := datastar.NewSSE(w, r)
	res, err := h.svc.VerifySnapshot(r.Context(), name)
	if notifyErr(sse, "Verify snapshot", err) {
		return
	}
	msg := "integrity OK"
	if res != nil && res.UpperDigest != "" {
		msg = res.UpperAlgo + ":" + views.ShortDigest(res.UpperDigest)
	}
	notify(sse, toast.VariantSuccess, "Snapshot verified", msg)
}

func (h *Handler) snapshotDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Delete snapshot", h.svc.RemoveSnapshot(r.Context(), name, true)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Snapshot deleted", views.ShortDigest(name))
	h.reRenderSnapshots(r, sse)
}

func (h *Handler) reRenderSnapshots(r *http.Request, sse *datastar.ServerSentEventGenerator) {
	s := parseSort(r, "created")
	rows, err := h.snapshotRows(r.Context(), s)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.SnapshotTable(rows, s))
}
