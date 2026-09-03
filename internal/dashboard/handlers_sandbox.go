package dashboard

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/mark3labs/msbd/internal/core"
	"github.com/mark3labs/msbd/internal/dashboard/components/toast"
	"github.com/mark3labs/msbd/internal/dashboard/views"
)

// ---- Sandboxes ----

func (h *Handler) sandboxTable(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	s := parseSort(r, "id")
	rows, err := h.sandboxRows(r.Context(), s)
	if notifyErr(sse, "List sandboxes", err) {
		return
	}
	_ = sse.PatchElementTempl(views.SandboxTable(rows, s))
}

func (h *Handler) sandboxRows(ctx context.Context, s views.TableSort) ([]views.SandboxRow, error) {
	list, err := h.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]views.SandboxRow, 0, len(list))
	for i := range list {
		rows = append(rows, toSandboxRow(&list[i]))
	}
	sortRows(rows, s, func(a, b views.SandboxRow) bool {
		switch s.Col {
		case "image":
			return a.Image < b.Image
		case "state":
			return a.State < b.State
		case "uptime":
			return a.CreatedAt.After(b.CreatedAt)
		default:
			return a.ID < b.ID
		}
	})
	return rows, nil
}

func (h *Handler) sandboxStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	if failInline(sse, "detail-error", "Start", h.svc.Start(r.Context(), id)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Started", id)
	h.refreshSandboxViews(r, sse, id)
}

func (h *Handler) sandboxStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	if failInline(sse, "detail-error", "Stop", h.svc.Stop(r.Context(), id)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Stopped", id)
	h.refreshSandboxViews(r, sse, id)
}

func (h *Handler) sandboxDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	if failInline(sse, "detail-error", "Delete", h.svc.Delete(r.Context(), id)) {
		return
	}
	notify(sse, toast.VariantSuccess, "Deleted", id)
	// Deleting from the detail page would leave the user on a dead URL.
	if r.URL.Query().Get("back") == "1" {
		_ = sse.Redirect("/sandboxes")
		return
	}
	h.refreshTable(r, sse)
}

// refreshSandboxViews updates whichever sandbox view is on screen: the list
// fragment (a no-op when absent) and the detail page's live header signals.
func (h *Handler) refreshSandboxViews(r *http.Request, sse *datastar.ServerSentEventGenerator, id string) {
	h.refreshTable(r, sse)
	if ins, err := h.svc.Get(r.Context(), id); err == nil {
		_ = sse.MarshalAndPatchSignals(&detailSignals{
			State:  ins.State,
			Uptime: fmtDuration(ins.UptimeSeconds),
		})
	}
}

// detailSignals mirrors the live header fields on the sandbox detail page.
type detailSignals struct {
	State  string `json:"sbxstate"`
	Uptime string `json:"sbxuptime"`
}

func (h *Handler) refreshTable(r *http.Request, sse *datastar.ServerSentEventGenerator) {
	s := parseSort(r, "id")
	rows, err := h.sandboxRows(r.Context(), s)
	if err != nil {
		return
	}
	_ = sse.PatchElementTempl(views.SandboxTable(rows, s))
}

type createSbxSignals struct {
	Image    string  `json:"sbximage"`
	CPU      float64 `json:"sbxcpu"`
	Memory   int     `json:"sbxmemory"`
	Disk     int     `json:"sbxdisk"`
	AutoStop int     `json:"sbxautostop"`
	Workdir  string  `json:"sbxworkdir"`
	Network  string  `json:"sbxnetwork"`
	Env      string  `json:"sbxenv"`
	Ports    string  `json:"sbxports"`
	Mounts   string  `json:"sbxmounts"`
	Secrets  string  `json:"sbxsecrets"`
	Labels   string  `json:"sbxlabels"`
	User     string  `json:"sbxuser"`
	Hostname string  `json:"sbxhostname"`
}

func (h *Handler) sandboxCreate(w http.ResponseWriter, r *http.Request) {
	sig := &createSbxSignals{}
	_ = datastar.ReadSignals(r, sig)
	sse := datastar.NewSSE(w, r)

	image := strings.TrimSpace(sig.Image)
	if image == "" {
		_ = sse.PatchElementTempl(views.InlineError("create-sandbox-error", "Image required", "Pick an OCI image to boot from."))
		return
	}
	ports, err := parsePorts(sig.Ports)
	if err != nil {
		_ = sse.PatchElementTempl(views.InlineError("create-sandbox-error", "Invalid port forward", err.Error()))
		return
	}
	mounts, err := parseMounts(sig.Mounts)
	if err != nil {
		_ = sse.PatchElementTempl(views.InlineError("create-sandbox-error", "Invalid mount", err.Error()))
		return
	}

	p := core.CreateParams{
		Image:         image,
		CPU:           sig.CPU,
		MemoryMB:      sig.Memory,
		DiskGB:        sig.Disk,
		AutoStopSecs:  sig.AutoStop,
		Workdir:       strings.TrimSpace(sig.Workdir),
		NetworkPolicy: sig.Network,
		Env:           parseEnv(sig.Env),
		Labels:        parseEnv(sig.Labels),
		User:          strings.TrimSpace(sig.User),
		Hostname:      strings.TrimSpace(sig.Hostname),
		Ports:         ports,
		Mounts:        mounts,
		Secrets:       parseSecrets(sig.Secrets),
	}
	ins, err := h.svc.Create(r.Context(), p)
	if failInline(sse, "create-sandbox-error", "Create sandbox", err) {
		return
	}
	closeDialog(sse, "create-sandbox")
	notify(sse, toast.VariantSuccess, "Sandbox created", ins.ID)
	h.refreshTable(r, sse)
}

// ---- Run (async jobs) ----

type runSignals struct {
	Cmd string `json:"runcmd"`
}

// sandboxRun launches the command as an async JOB and streams its output into
// the run panel over the open SSE connection. Unlike a blocking /run call this
// keeps the UI responsive, shows partial output as it arrives, and lets the
// user cancel a command that never returns.
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

	jobID, err := h.svc.Launch(r.Context(), id, core.ExecParams{Cmd: cmd})
	if failInline(sse, "detail-error", "Run", err) {
		return
	}
	_ = sse.MarshalAndPatchSignals(&runSignals{Cmd: ""})

	started := time.Now()
	view := views.JobView{SandboxID: id, JobID: jobID, Cmd: cmd, State: "running", Started: started}
	view.Elapsed = "0s"
	// Newest run on top.
	_ = sse.PatchElementTempl(views.RunBlock(view),
		datastar.WithSelectorID("run-history"), datastar.WithModePrepend())

	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	for {
		st, err := h.svc.Poll(id, jobID)
		if err != nil {
			view.State = "gone"
			view.Done = true
			view.Stderr = cleanErr(err)
			_ = sse.PatchElementTempl(views.RunBlock(view))
			return
		}
		view.State = st.State
		view.ExitCode = st.ExitCode
		view.Stdout = st.Stdout
		view.Stderr = st.Stderr
		view.Truncated = st.Truncated
		view.Elapsed = fmtDuration(time.Since(started).Seconds())
		view.Done = st.State != "running"
		if view.Done && (h.takeCancelled(id, jobID) || st.State == core.JobKilled) {
			// A killed job reports exit 0; say what actually happened.
			view.Cancelled = true
		}

		if err := sse.PatchElementTempl(views.RunBlock(view)); err != nil || sse.IsClosed() {
			return
		}
		if view.Done {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// sandboxJobCancel terminates a running job started from the Run panel.
func (h *Handler) sandboxJobCancel(w http.ResponseWriter, r *http.Request) {
	id, job := r.PathValue("id"), r.PathValue("job")
	sse := datastar.NewSSE(w, r)
	h.markCancelled(id, job)
	if notifyErr(sse, "Cancel", h.svc.CancelJob(id, job)) {
		h.takeCancelled(id, job)
		return
	}
	notify(sse, toast.VariantInfo, "Command cancelled", job)
}

// ---- Logs ----

type logSignals struct {
	Source string `json:"logsrc"`
	Tail   string `json:"logtail"`
}

func (h *Handler) logQuery(r *http.Request) core.LogQuery {
	sig := &logSignals{}
	_ = datastar.ReadSignals(r, sig)
	q := core.LogQuery{Tail: 200}
	if n, err := strconv.ParseUint(strings.TrimSpace(sig.Tail), 10, 64); err == nil {
		q.Tail = n
	}
	if s := strings.TrimSpace(sig.Source); s != "" {
		q.Sources = []string{s}
	}
	return q
}

func (h *Handler) sandboxLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sse := datastar.NewSSE(w, r)
	entries, err := h.svc.Logs(r.Context(), id, h.logQuery(r))
	if notifyErr(sse, "Logs", err) {
		return
	}
	_ = sse.PatchElementTempl(views.LogsPanel(toLogLines(entries)))
}

// sandboxLogsDownload streams the full log as a plain-text attachment.
func (h *Handler) sandboxLogsDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	entries, err := h.svc.Logs(r.Context(), id, core.LogQuery{})
	if err != nil {
		http.Error(w, cleanErr(err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeFilename(id)+".log\"")
	for _, e := range entries {
		ts := ""
		if !e.Timestamp.IsZero() {
			ts = e.Timestamp.Format(time.RFC3339) + " "
		}
		_, _ = w.Write([]byte(ts + e.Source + " " + e.Text + "\n"))
	}
}

func toLogLines(entries []core.LogEntry) []views.LogLine {
	lines := make([]views.LogLine, 0, len(entries))
	for _, e := range entries {
		lines = append(lines, views.LogLine{
			Source:    e.Source,
			Timestamp: e.Timestamp,
			Text:      e.Text,
		})
	}
	return lines
}

// ---- Metrics ----

// metricSignals are the live numbers streamed into the client signal store.
// All names are flat lowercase to dodge Datastar's camelCase/kebab attribute
// round-trip pitfalls. `mtick` is a monotonic counter bumped every push: it
// always changes, so the <metric-chart> components advance one sample per
// second even when a value is flat. Raw values (bytes, %) — the web component
// formats + rate-converts.
//
// The same stream carries the sandbox state/uptime so the detail page header
// stays live without a second poll.
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
	State    string  `json:"sbxstate"`
	Uptime   string  `json:"sbxuptime"`
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
		state, uptime := "", ""
		if ins, err := h.svc.Get(r.Context(), id); err == nil {
			state, uptime = ins.State, fmtDuration(ins.UptimeSeconds)
		}
		var patchErr error
		if m, err := h.svc.Metrics(r.Context(), id); err != nil {
			msg := "metrics unavailable"
			if errors.Is(err, core.ErrNotFound) {
				msg = "sandbox not found"
			} else if e := err.Error(); e != "" {
				msg = e
			}
			patchErr = sse.MarshalAndPatchSignals(&metricSignals{
				Tick: tick, Ok: false, Err: msg, State: state, Uptime: uptime,
			})
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
				State:    state,
				Uptime:   uptime,
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

// ---- mapping helpers ----

func toSandboxRow(i *core.Instance) views.SandboxRow {
	return views.SandboxRow{
		ID:        i.ID,
		Image:     i.Image,
		State:     i.State,
		Workdir:   i.Workdir,
		Uptime:    fmtDuration(i.UptimeSeconds),
		Labels:    i.Labels,
		CreatedAt: i.CreatedAt,
	}
}
