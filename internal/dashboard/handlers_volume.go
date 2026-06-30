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

// ---- Volumes ----

func (h *Handler) volumeList(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rows, err := h.volumeRows(r.Context())
	if notifyErr(sse, "List volumes", err) {
		return
	}
	_ = sse.PatchElementTempl(views.VolumesPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}

func (h *Handler) volumeRows(ctx context.Context) ([]views.VolumeRow, error) {
	vols, err := h.svc.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.VolumeRow, 0, len(vols))
	for i := range vols {
		v := &vols[i]
		capacity := "—"
		if v.CapacityBytes != nil {
			capacity = views.HumanBytes(*v.CapacityBytes)
		}
		rows = append(rows, views.VolumeRow{
			Name:      v.Name,
			Path:      v.Path,
			Kind:      v.Kind,
			Used:      views.HumanBytes(v.UsedBytes),
			Capacity:  capacity,
			CreatedAt: v.CreatedAt.Format(time.RFC3339),
		})
	}
	return rows, nil
}

type createVolSignals struct {
	Name  string `json:"volname"`
	Kind  string `json:"volkind"`
	Size  int    `json:"volsize"`
	Quota int    `json:"volquota"`
}

func (h *Handler) volumeCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createVolSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	name := strings.TrimSpace(sig.Name)
	if name == "" {
		notify(sse, toast.VariantWarning, "Create volume", "name is required")
		return
	}
	_, err := h.svc.CreateVolume(r.Context(), core.VolumeParams{
		Name:     name,
		Kind:     sig.Kind,
		SizeMiB:  sig.Size,
		QuotaMiB: sig.Quota,
	})
	if notifyErr(sse, "Create volume", err) {
		return
	}
	closeDialog(sse, "create-volume")
	notify(sse, toast.VariantSuccess, "Created", name)
	h.reRenderVolumes(r.Context(), sse)
}

func (h *Handler) volumeDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Delete volume", h.svc.RemoveVolume(r.Context(), name)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Deleted", name)
	h.reRenderVolumes(r.Context(), sse)
}

func (h *Handler) reRenderVolumes(ctx context.Context, sse *datastar.ServerSentEventGenerator) {
	rows, err := h.volumeRows(ctx)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.VolumesPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}
