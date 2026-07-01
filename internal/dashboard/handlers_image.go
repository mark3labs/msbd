package dashboard

import (
	"context"
	"net/http"
	"strings"
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

type pullImageSignals struct {
	Reference string `json:"imgref"`
	Force     bool   `json:"imgforce"`
}

// imagePull fetches an image into the cache. The pull boots a throwaway microVM
// and can take minutes, so we toast "pulling…" up front, run the (blocking) pull
// on the open SSE connection, then toast the result and re-render the list.
// Datastar keeps the SSE stream open for the duration, so the UI stays live.
func (h *Handler) imagePull(w http.ResponseWriter, r *http.Request) {
	sig := &pullImageSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	ref := strings.TrimSpace(sig.Reference)
	if ref == "" {
		notify(sse, toast.VariantWarning, "Pull image", "reference is required")
		return
	}
	closeDialog(sse, "pull-image")
	notify(sse, toast.VariantInfo, "Pulling", ref+" — this can take a while")

	img, err := h.svc.PullImage(r.Context(), ref, sig.Force)
	if notifyErr(sse, "Pull image", err) {
		return
	}
	notify(sse, toast.VariantSuccess, "Pulled", img.Reference)
	h.reRenderImages(r.Context(), sse)
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
