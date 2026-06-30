package dashboard

import (
	"context"
	"net/http"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// ---- Images ----

func (h *Handler) imageList(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rows, err := h.imageRows(r.Context())
	if notifyErr(sse, "List images", err) {
		return
	}
	_ = sse.PatchElementTempl(views.ImagesPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}

func (h *Handler) imageRows(ctx context.Context) ([]views.ImageRow, error) {
	imgs, err := h.svc.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.ImageRow, 0, len(imgs))
	for i := range imgs {
		im := &imgs[i]
		size := "—"
		if im.SizeBytes != nil && *im.SizeBytes >= 0 {
			size = views.HumanBytes(uint64(*im.SizeBytes))
		}
		rows = append(rows, views.ImageRow{
			Reference:    im.Reference,
			Architecture: im.Architecture,
			OS:           im.OS,
			Layers:       im.LayerCount,
			Size:         size,
			LastUsedAt:   im.LastUsedAt.Format(time.RFC3339),
		})
	}
	return rows, nil
}

func (h *Handler) imageRemove(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Remove image", h.svc.RemoveImage(r.Context(), ref, true)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Removed", ref)
	h.reRenderImages(r.Context(), sse)
}

func (h *Handler) imagePrune(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rep, err := h.svc.PruneImages(r.Context())
	if notifyErr(sse, "Prune images", err) {
		return
	}
	msg := "nothing to reclaim"
	if rep != nil {
		msg = "removed image refs / layers"
	}
	notify(sse, toast.VariantSuccess, "Pruned", msg)
	h.reRenderImages(r.Context(), sse)
}

func (h *Handler) reRenderImages(ctx context.Context, sse *datastar.ServerSentEventGenerator) {
	rows, err := h.imageRows(ctx)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.ImagesPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}
