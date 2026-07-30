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

// ---- Volumes ----

func (h *Handler) volumeTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	s := parseSort(r, "name")
	rows, err := h.volumeRows(r.Context(), s)
	if notifyErr(sse, "List volumes", err) {
		return
	}
	_ = sse.PatchElementTempl(views.VolumeTable(rows, s))
}

func (h *Handler) volumeRows(ctx context.Context, s views.TableSort) ([]views.VolumeRow, error) {
	vols, err := h.svc.ListVolumes(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.VolumeRow, 0, len(vols))
	for i := range vols {
		v := &vols[i]
		capacity, usedPct := "—", -1
		if v.CapacityBytes != nil && *v.CapacityBytes > 0 {
			capacity = views.HumanBytes(*v.CapacityBytes)
			usedPct = min(100, int(v.UsedBytes*100 / *v.CapacityBytes))
		}
		rows = append(rows, views.VolumeRow{
			Name:        v.Name,
			Path:        v.Path,
			Kind:        v.Kind,
			Used:        views.HumanBytes(v.UsedBytes),
			Capacity:    capacity,
			UsedPercent: usedPct,
			CreatedAt:   v.CreatedAt,
			Labels:      v.Labels,
		})
	}
	sortRows(rows, s, func(a, b views.VolumeRow) bool {
		switch s.Col {
		case "used":
			return a.Used < b.Used
		case "created":
			return a.CreatedAt.Before(b.CreatedAt)
		default:
			return a.Name < b.Name
		}
	})
	return rows, nil
}

type createVolSignals struct {
	Name   string `json:"volname"`
	Kind   string `json:"volkind"`
	Size   int    `json:"volsize"`
	Quota  int    `json:"volquota"`
	Labels string `json:"vollabels"`
}

func (h *Handler) volumeCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createVolSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	name := strings.TrimSpace(sig.Name)
	if name == "" {
		_ = sse.PatchElementTempl(views.InlineError("create-volume-error", "Name required", "Give the volume a name."))
		return
	}
	_, err := h.svc.CreateVolume(r.Context(), core.VolumeParams{
		Name:     name,
		Kind:     sig.Kind,
		SizeMiB:  sig.Size,
		QuotaMiB: sig.Quota,
		Labels:   parseEnv(sig.Labels),
	})
	if failInline(sse, "create-volume-error", "Create volume", err) {
		return
	}
	closeDialog(sse, "create-volume")
	notify(sse, toast.VariantSuccess, "Volume created", name)
	h.reRenderVolumes(r, sse)
}

func (h *Handler) volumeDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Delete volume", h.svc.RemoveVolume(r.Context(), name)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Volume deleted", name)
	h.reRenderVolumes(r, sse)
}

func (h *Handler) reRenderVolumes(r *http.Request, sse *datastar.ServerSentEventGenerator) {
	s := parseSort(r, "name")
	rows, err := h.volumeRows(r.Context(), s)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.VolumeTable(rows, s))
}
