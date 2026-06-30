package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// ---- Sandboxes ----

func (h *Handler) sandboxList(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rows, err := h.sandboxRows(r.Context())
	if notifyErr(sse, "List sandboxes", err) {
		return
	}
	_ = sse.PatchElementTempl(views.SandboxesPage(rows), datastar.WithSelectorID("content"), datastar.WithModeInner())
}

func (h *Handler) sandboxTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	rows, err := h.sandboxRows(r.Context())
	if notifyErr(sse, "List sandboxes", err) {
		return
	}
	_ = sse.PatchElementTempl(views.SandboxTable(rows))
}

func (h *Handler) sandboxRows(ctx context.Context) ([]views.SandboxRow, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.SandboxRow, 0, len(list))
	for i := range list {
		rows = append(rows, toSandboxRow(&list[i]))
	}
	return rows, nil
}

func (h *Handler) sandboxDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	ins, err := h.svc.Inspect(r.Context(), id)
	if notifyErr(sse, "Inspect", err) {
		return
	}
	d := views.SandboxDetail{
		SandboxRow: toSandboxRow(&ins.Instance),
		Config:     prettyJSON(ins.ConfigJSON),
	}
	_ = sse.PatchElementTempl(views.SandboxDetailPage(d), datastar.WithSelectorID("content"), datastar.WithModeInner())
}

func (h *Handler) sandboxStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Start", h.svc.Start(r.Context(), id)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Started", id)
	h.refreshTable(r.Context(), sse)
}

func (h *Handler) sandboxStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Stop", h.svc.Stop(r.Context(), id)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Stopped", id)
	h.refreshTable(r.Context(), sse)
}

func (h *Handler) sandboxDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	if notifyErr(sse, "Delete", h.svc.Delete(r.Context(), id)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Deleted", id)
	h.refreshTable(r.Context(), sse)
}

func (h *Handler) refreshTable(ctx context.Context, sse *datastar.ServerSentEventGenerator) {
	rows, err := h.sandboxRows(ctx)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.SandboxTable(rows))
}

type createSbxSignals struct {
	Image   string  `json:"sbximage"`
	CPU     float64 `json:"sbxcpu"`
	Memory  int     `json:"sbxmemory"`
	Disk    int     `json:"sbxdisk"`
	Workdir string  `json:"sbxworkdir"`
	Network string  `json:"sbxnetwork"`
	Env     string  `json:"sbxenv"`
}

func (h *Handler) sandboxCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createSbxSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	p := core.CreateParams{
		Image:         strings.TrimSpace(sig.Image),
		CPU:           sig.CPU,
		MemoryMB:      sig.Memory,
		DiskGB:        sig.Disk,
		Workdir:       strings.TrimSpace(sig.Workdir),
		NetworkPolicy: sig.Network,
		Env:           parseEnv(sig.Env),
	}
	ins, err := h.svc.Create(r.Context(), p)
	if notifyErr(sse, "Create sandbox", err) {
		return
	}
	closeDialog(sse, "create-sandbox")
	notify(sse, toast.VariantSuccess, "Created", ins.ID)
	h.refreshTable(r.Context(), sse)
}

type runSignals struct {
	Cmd string `json:"runcmd"`
}

func (h *Handler) sandboxRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sig := &runSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	cmd := strings.TrimSpace(sig.Cmd)
	if cmd == "" {
		notify(sse, toast.VariantWarning, "Run", "command is empty")
		return
	}
	res, err := h.svc.Run(r.Context(), id, core.ExecParams{Cmd: cmd})
	if notifyErr(sse, "Run", err) {
		return
	}
	_ = sse.PatchElementTempl(views.RunOutput(res.ExitCode, res.Stdout, res.Stderr))
}

func (h *Handler) sandboxLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	entries, err := h.svc.Logs(r.Context(), id, core.LogQuery{Tail: 200})
	if notifyErr(sse, "Logs", err) {
		return
	}
	lines := make([]views.LogLine, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, views.LogLine{
			Source:    e.Source,
			Timestamp: e.Timestamp.Format(time.RFC3339),
			Text:      e.Text,
		})
	}
	_ = sse.PatchElementTempl(views.LogsPanel(lines))
}

// metricSignals are the live numbers streamed into the client signal store.
// All names are flat lowercase to dodge Datastar's camelCase/kebab attribute
// round-trip pitfalls (signals live in attribute *values*, but keeping them
// single-token lowercase means the data-attr *keys* that read them are trivial
// too). `mtick` is a monotonic counter bumped every push: it always changes, so
// the <metric-chart> components advance one sample per second even when a value
// is flat. Raw values (bytes, %) — the web component formats + rate-converts.
type metricSignals struct {
	Cpu      float64 `json:"mcpu"`
	MemUsed  uint64  `json:"mmemused"`
	MemLimit uint64  `json:"mmemlimit"`
	DiskR    uint64  `json:"mdiskr"`
	DiskW    uint64  `json:"mdiskw"`
	NetRx    uint64  `json:"mnetrx"`
	NetTx    uint64  `json:"mnettx"`
	Tick     uint64  `json:"mtick"`
	Ok       bool    `json:"mok"`
	Err      string  `json:"merr"`
}

// sandboxMetricsStream is a SINGLE long-lived SSE connection (opened once when
// the detail view loads, via data-init). It patches the metric signals every
// second until the client disconnects (navigates away → Datastar aborts the
// fetch → the request context cancels). This beats per-second @get polling: no
// connection churn, and the charts keep their history because we only ever
// patch SIGNALS, never re-render the chart elements.
func (h *Handler) sandboxMetricsStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	var tick uint64
	for {
		tick++
		var patchErr error
		if m, err := h.svc.Metrics(r.Context(), id); err != nil {
			msg := "metrics unavailable"
			if errors.Is(err, core.ErrNotFound) {
				msg = "sandbox not found"
			} else if e := err.Error(); e != "" {
				msg = e
			}
			patchErr = sse.MarshalAndPatchSignals(&metricSignals{Tick: tick, Ok: false, Err: msg})
		} else {
			patchErr = sse.MarshalAndPatchSignals(&metricSignals{
				Cpu:      m.CPUPercent,
				MemUsed:  m.MemoryBytes,
				MemLimit: m.MemoryLimitBytes,
				DiskR:    m.DiskReadBytes,
				DiskW:    m.DiskWriteBytes,
				NetRx:    m.NetRxBytes,
				NetTx:    m.NetTxBytes,
				Tick:     tick,
				Ok:       true,
			})
		}
		if patchErr != nil || sse.IsClosed() {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

type filesSignals struct {
	Path string `json:"filepath"`
}

func (h *Handler) sandboxFiles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sig := &filesSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	entries, err := h.svc.ListDir(r.Context(), id, strings.TrimSpace(sig.Path), "")
	if notifyErr(sse, "List files", err) {
		return
	}
	rows := make([]views.FileRow, 0, len(entries))
	for _, e := range entries {
		kind := "file"
		if e.Kind == "directory" {
			kind = "dir"
		}
		rows = append(rows, views.FileRow{
			Path: e.Path,
			Kind: kind,
			Size: views.HumanBytes(uint64(maxZero(e.Size))),
			Mode: fmt.Sprintf("%#o", e.Mode),
		})
	}
	_ = sse.PatchElementTempl(views.FilesPanel(rows))
}

// ---- mapping helpers ----

func toSandboxRow(i *core.Instance) views.SandboxRow {
	return views.SandboxRow{
		ID:      i.ID,
		Image:   i.Image,
		State:   i.State,
		Workdir: i.Workdir,
		Uptime:  fmtDuration(i.UptimeSeconds),
		Labels:  i.Labels,
	}
}
