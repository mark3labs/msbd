package dashboard

// handlers_image.go — the local OCI image cache: list, inspect, pull/re-pull,
// remove and prune.
//
// Pulling is the awkward one. The microsandbox SDK has no standalone pull, so
// core.PullImage boots a throwaway microVM with a pull policy; a cold pull can
// run for minutes. The handler therefore does NOT block the UI: it closes the
// dialog immediately, streams elapsed-time progress into #pull-progress, and
// runs the pull on a context detached from the browser request so navigating
// away (or closing the tab) cannot abort a half-finished download.

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

// ---- Images ----

func (h *Handler) imageTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	s := parseSort(r, "reference")
	rows, err := h.imageRows(r.Context(), s)
	if notifyErr(sse, "List images", err) {
		return
	}
	_ = sse.PatchElementTempl(views.ImageTable(rows, s))
}

func (h *Handler) imageRows(ctx context.Context, s views.TableSort) ([]views.ImageRow, error) {
	imgs, err := h.svc.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	// Mark images a live sandbox was booted from, so "safe to delete" is
	// obvious before the user reaches for the trash icon.
	inUse := map[string]bool{}
	if list, err := h.svc.List(ctx); err == nil {
		for i := range list {
			inUse[list[i].Image] = true
		}
	}
	rows := make([]views.ImageRow, 0, len(imgs))
	for i := range imgs {
		im := &imgs[i]
		size, bytes := "—", uint64(0)
		if im.SizeBytes != nil && *im.SizeBytes >= 0 {
			bytes = uint64(*im.SizeBytes)
			size = views.HumanBytes(bytes)
		}
		rows = append(rows, views.ImageRow{
			Reference:    im.Reference,
			Digest:       im.ManifestDigest,
			Architecture: im.Architecture,
			OS:           im.OS,
			Layers:       im.LayerCount,
			Size:         size,
			SizeBytes:    bytes,
			CreatedAt:    im.CreatedAt,
			LastUsedAt:   im.LastUsedAt,
			InUse:        inUse[im.Reference],
		})
	}
	sortRows(rows, s, func(a, b views.ImageRow) bool {
		switch s.Col {
		case "size":
			return a.SizeBytes < b.SizeBytes
		case "used":
			return a.LastUsedAt.Before(b.LastUsedAt)
		default:
			return a.Reference < b.Reference
		}
	})
	return rows, nil
}

// imageInspect opens the read-only OCI config + layer breakdown.
func (h *Handler) imageInspect(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	sse := datastar.NewSSE(w, r)
	d, err := h.svc.InspectImage(r.Context(), ref)
	if notifyErr(sse, "Inspect image", err) {
		return
	}
	size, bytes := "—", uint64(0)
	if d.SizeBytes != nil && *d.SizeBytes >= 0 {
		bytes = uint64(*d.SizeBytes)
		size = views.HumanBytes(bytes)
	}
	v := views.ImageDetailView{
		ImageRow: views.ImageRow{
			Reference:    d.Reference,
			Digest:       d.ManifestDigest,
			Architecture: d.Architecture,
			OS:           d.OS,
			Layers:       d.LayerCount,
			Size:         size,
			SizeBytes:    bytes,
			CreatedAt:    d.CreatedAt,
			LastUsedAt:   d.LastUsedAt,
		},
	}
	if d.Config != nil {
		v.Entrypoint = strings.Join(d.Config.Entrypoint, " ")
		v.Cmd = strings.Join(d.Config.Cmd, " ")
		v.WorkingDir = d.Config.WorkingDir
		v.User = d.Config.User
		v.Env = d.Config.Env
	}
	for _, l := range d.Layers {
		var sz int64
		if l.CompressedSizeBytes != nil {
			sz = *l.CompressedSizeBytes
		}
		v.LayerRows = append(v.LayerRows, views.ImageLayerRow{
			Digest: l.BlobDigest,
			Size:   views.HumanBytes(uint64(maxZero(sz))),
			Media:  l.MediaType,
		})
	}
	_ = sse.PatchElementTempl(views.ImageDetailDialog(v))
	_ = sse.ExecuteScript("window.tui?.dialog?.open('image-detail')")
}

type pullImageSignals struct {
	Reference string `json:"imgref"`
	Force     bool   `json:"imgforce"`
}

// imagePull fetches an image into the cache.
//
// Two entry points share this handler: the Pull dialog (reference + force come
// from signals) and the per-row Re-pull action (?ref=&force=1), which is the
// one-click way to refresh a moving tag like :latest.
func (h *Handler) imagePull(w http.ResponseWriter, r *http.Request) {
	sig := &pullImageSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	ref := strings.TrimSpace(sig.Reference)
	force := sig.Force
	// Query params win: they come from the row action, where there is no form.
	if q := strings.TrimSpace(r.URL.Query().Get("ref")); q != "" {
		ref = q
		force = r.URL.Query().Get("force") == "1"
	}
	if ref == "" {
		_ = sse.PatchElementTempl(views.InlineError("pull-image-error", "Reference required", "e.g. microsandbox/python"))
		return
	}

	// Record the cached digest first so we can tell "fetched a newer copy" from
	// "already up to date" — the whole point of a force re-pull.
	priorDigest, wasCached := "", false
	if prior, err := h.svc.InspectImage(r.Context(), ref); err == nil {
		priorDigest, wasCached = prior.ManifestDigest, true
	}
	if wasCached && !force {
		closeDialog(sse, "pull-image")
		notify(sse, toast.VariantInfo, "Already cached", ref+" is in the local cache. Use Force (or Re-pull) to fetch a newer copy.")
		return
	}

	closeDialog(sse, "pull-image")

	// Detach from the browser request: a multi-minute pull must survive the user
	// navigating away or closing the tab. core.PullImage applies its own
	// PullTimeout budget on top of this.
	pullCtx := context.WithoutCancel(r.Context())
	type result struct {
		img *core.Image
		err error
	}
	done := make(chan result, 1)
	go func() {
		img, err := h.svc.PullImage(pullCtx, ref, force)
		done <- result{img, err}
	}()

	started := time.Now()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	_ = sse.PatchElementTempl(views.PullProgress(ref, "0s"))

	for {
		select {
		case res := <-done:
			_ = sse.PatchElementTempl(views.PullDone())
			if res.err != nil {
				// Registry failures are the common case here (typos, private
				// repos, rate limits), and the raw SDK text is cryptic, so
				// attach a hint about what to actually do next.
				msg := cleanErr(res.err)
				if hint := pullErrorHint(msg); hint != "" {
					msg += "\n\n" + hint
				}
				notify(sse, toast.VariantError, "Pull failed", msg)
				_ = sse.PatchElementTempl(views.InlineError("pull-image-error", "Pull failed", msg))
				return
			}
			h.notifyPullOutcome(sse, res.img, priorDigest, wasCached, time.Since(started))
			h.reRenderImages(r, sse)
			return
		case <-ticker.C:
			// The client went away; the pull itself keeps running on pullCtx.
			if sse.IsClosed() {
				return
			}
			if err := sse.PatchElementTempl(views.PullProgress(ref, fmtDuration(time.Since(started).Seconds()))); err != nil {
				return
			}
		}
	}
}

// notifyPullOutcome reports what the pull actually accomplished, which for a
// moving tag is the only way to know whether anything changed.
func (h *Handler) notifyPullOutcome(sse *datastar.ServerSentEventGenerator, img *core.Image, priorDigest string, wasCached bool, took time.Duration) {
	if img == nil {
		notify(sse, toast.VariantSuccess, "Pull complete", "took "+fmtDuration(took.Seconds()))
		return
	}
	switch {
	case !wasCached:
		notify(sse, toast.VariantSuccess, "Image pulled",
			img.Reference+" is now cached ("+imgSize(img)+", took "+fmtDuration(took.Seconds())+").")
	case priorDigest != "" && img.ManifestDigest != priorDigest:
		notify(sse, toast.VariantSuccess, "Image updated",
			img.Reference+" fetched a newer copy — digest "+views.ShortDigest(priorDigest)+" → "+views.ShortDigest(img.ManifestDigest)+".")
	default:
		notify(sse, toast.VariantInfo, "Already up to date",
			img.Reference+" is unchanged — the registry copy matches the cached one.")
	}
}

// pullErrorHint maps an opaque registry error onto something the user can act
// on. Docker Hub in particular answers "Not authorized" for a typo, a private
// repository AND anonymous rate limiting alike (it will not confirm whether a
// repo exists), so that hint has to cover all three.
func pullErrorHint(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "not authorized"), strings.Contains(m, "unauthorized"), strings.Contains(m, "401"):
		return "Registries answer this for a mistyped reference, a private repository, or anonymous rate limiting. " +
			"Double-check the reference and tag; if they are right, you are probably rate limited — wait a few minutes and retry."
	case strings.Contains(m, "manifest unknown"), strings.Contains(m, "not found"), strings.Contains(m, "404"):
		return "That reference does not exist in the registry. Check the repository name and tag."
	case strings.Contains(m, "deadline exceeded"), strings.Contains(m, "timeout"), strings.Contains(m, "timed out"):
		return "The pull exceeded its time budget. Large images may need a longer --pull-timeout."
	case strings.Contains(m, "no such host"), strings.Contains(m, "dial tcp"), strings.Contains(m, "connection refused"),
		strings.Contains(m, "network is unreachable"):
		return "The registry was unreachable. Check the daemon host's network access and any proxy settings."
	case strings.Contains(m, "no space left"):
		return "The image cache filled the disk. Prune unused images and retry."
	default:
		return ""
	}
}

func imgSize(img *core.Image) string {
	if img.SizeBytes != nil && *img.SizeBytes >= 0 {
		return views.HumanBytes(uint64(*img.SizeBytes))
	}
	return "size unknown"
}

func (h *Handler) imageRemove(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Remove image", h.svc.RemoveImage(r.Context(), ref, true)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Image removed", ref)
	h.reRenderImages(r, sse)
}

// imagePrune reports exactly what it reclaimed instead of a vague success.
func (h *Handler) imagePrune(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rep, err := h.svc.PruneImages(r.Context())
	if notifyErr(sse, "Prune images", err) {
		return
	}
	notify(sse, toast.VariantSuccess, "Prune complete", pruneSummary(rep))
	h.reRenderImages(r, sse)
}

// pruneSummary turns the prune report into a human sentence. The old UI threw
// all of these numbers away.
func pruneSummary(rep *core.ImagePruneReport) string {
	if rep == nil {
		return "Nothing to reclaim."
	}
	parts := []string{}
	if rep.ImageRefsRemoved > 0 {
		parts = append(parts, plural(int(rep.ImageRefsRemoved), "image ref"))
	}
	if rep.ManifestsRemoved > 0 {
		parts = append(parts, plural(int(rep.ManifestsRemoved), "manifest"))
	}
	if rep.LayersRemoved > 0 {
		parts = append(parts, plural(int(rep.LayersRemoved), "layer"))
	}
	if rep.VMDKRemoved > 0 {
		parts = append(parts, plural(int(rep.VMDKRemoved), "disk image"))
	}
	if len(parts) == 0 {
		return "Nothing to reclaim."
	}
	msg := "Removed " + strings.Join(parts, ", ")
	if rep.BytesReclaimed != nil && *rep.BytesReclaimed > 0 {
		msg += " — " + views.HumanBytes(*rep.BytesReclaimed) + " reclaimed"
	}
	return msg + "."
}

func (h *Handler) reRenderImages(r *http.Request, sse *datastar.ServerSentEventGenerator) {
	s := parseSort(r, "reference")
	rows, err := h.imageRows(r.Context(), s)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.ImageTable(rows, s))
}
