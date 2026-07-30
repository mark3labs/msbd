package dashboard

// handlers_overview.go — the landing page: fleet counts, capacity headroom and
// aggregate resource use, assembled from data core already exposes but that the
// UI previously never surfaced (AllMetrics, MaxSandboxes, ActiveJobs).

import (
	"context"
	"net/http"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/dashboard/views"
)

func (h *Handler) overviewFragment(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	_ = sse.PatchElementTempl(views.OverviewBody(h.overviewData(r.Context())))
}

func (h *Handler) overviewData(ctx context.Context) views.OverviewData {
	d := views.OverviewData{
		MaxSandboxes: h.svc.MaxSandboxes(),
		ActiveJobs:   h.svc.ActiveJobs(),
	}

	list, err := h.svc.List(ctx)
	if err == nil {
		d.Total = len(list)
		rows := make([]views.SandboxRow, 0, len(list))
		for i := range list {
			switch list[i].State {
			case "running":
				d.Running++
			case "stopped", "paused":
				d.Stopped++
			case "error", "failed", "crashed":
				d.Failed++
			}
			rows = append(rows, toSandboxRow(&list[i]))
		}
		// Newest first, capped at five.
		sortRows(rows, views.TableSort{Dir: "desc"}, func(a, b views.SandboxRow) bool {
			return a.CreatedAt.Before(b.CreatedAt)
		})
		d.Recent = rows[:min(len(rows), 5)]
	}
	if d.MaxSandboxes > 0 {
		d.CapPercent = min(100, d.Running*100/d.MaxSandboxes)
	}

	// Aggregate live resource use across every running sandbox.
	if ms, err := h.svc.AllMetrics(ctx); err == nil && len(ms) > 0 {
		var cpu float64
		var used, limit uint64
		for i := range ms {
			cpu += ms[i].CPUPercent
			used += ms[i].MemoryBytes
			limit += ms[i].MemoryLimitBytes
		}
		d.MetricsOK = true
		d.CPUPercent = cpu
		d.MemUsed = views.HumanBytes(used)
		d.MemLimit = views.HumanBytes(limit)
		if limit > 0 {
			d.MemPercent = min(100, int(used*100/limit))
		}
	} else if d.Running == 0 {
		d.MetricsNote = "No running sandboxes."
	} else {
		d.MetricsNote = "Metrics unavailable."
	}

	if vols, err := h.svc.ListVolumes(ctx); err == nil {
		d.Volumes = len(vols)
		var used uint64
		for i := range vols {
			used += vols[i].UsedBytes
		}
		d.VolumesUsed = views.HumanBytes(used)
	}
	if imgs, err := h.svc.ListImages(ctx); err == nil {
		d.Images = len(imgs)
		var total uint64
		for i := range imgs {
			if imgs[i].SizeBytes != nil && *imgs[i].SizeBytes > 0 {
				total += uint64(*imgs[i].SizeBytes)
			}
		}
		d.ImagesSize = views.HumanBytes(total)
	}
	if snaps, err := h.svc.ListSnapshots(ctx); err == nil {
		d.Snapshots = len(snaps)
		var total uint64
		for i := range snaps {
			if snaps[i].SizeBytes != nil {
				total += *snaps[i].SizeBytes
			}
		}
		d.SnapshotSize = views.HumanBytes(total)
	}
	return d
}
