package api

// handlers.go — the route handlers. Each translates a DTO ⇄ core call and is a
// near-1:1 image of a sandbox.Provider method.

import (
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/mark3labs/msbd/internal/core"
)

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	rt := ""
	if v, err := core.RuntimeVersion(); err == nil {
		rt = v
	}
	writeJSON(w, http.StatusOK, VersionDTO{
		DefaultImage:   s.svc.DefaultImage(),
		RuntimeVersion: rt,
		SDKVersion:     core.SDKVersion(),
	})
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ports := make([]core.PortMapping, 0, len(req.Ports))
	for _, p := range req.Ports {
		ports = append(ports, core.PortMapping{HostPort: p.HostPort, GuestPort: p.GuestPort, Protocol: p.Protocol})
	}
	secrets := make([]core.SecretParam, 0, len(req.Secrets))
	for _, se := range req.Secrets {
		secrets = append(secrets, core.SecretParam{EnvVar: se.EnvVar, Value: se.Value})
	}
	mounts := make([]core.MountParam, 0, len(req.Mounts))
	for _, m := range req.Mounts {
		mounts = append(mounts, core.MountParam{GuestPath: m.GuestPath, Volume: m.Volume, Readonly: m.Readonly})
	}
	inst, err := s.svc.Create(r.Context(), core.CreateParams{
		Image:         req.Image,
		CPU:           req.Resources.CPU,
		MemoryMB:      req.Resources.MemoryMB,
		DiskGB:        req.Resources.DiskGB,
		AutoStopSecs:  req.AutoStopSecs,
		Env:           req.Env,
		Labels:        req.Labels,
		Workdir:       req.Workdir,
		User:          req.User,
		Hostname:      req.Hostname,
		NetworkPolicy: req.NetworkPolicy,
		Ports:         ports,
		Secrets:       secrets,
		Mounts:        mounts,
	})
	if err != nil {
		writeErr(w, http.StatusInsufficientStorage, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toInstanceDTO(inst))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	inst, err := s.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toInstanceDTO(inst))
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]InstanceDTO, 0, len(list))
	for i := range list {
		out = append(out, *toInstanceDTO(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Stop(r.Context(), r.PathValue("id")); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Start(r.Context(), r.PathValue("id")); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Delete(r.Context(), r.PathValue("id")); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) { s.execOrRun(w, r, false) }
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request)  { s.execOrRun(w, r, true) }

func (s *Server) execOrRun(w http.ResponseWriter, r *http.Request, long bool) {
	var req ExecRequestDTO
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	p := core.ExecParams{
		Cmd:     req.Cmd,
		Cwd:     req.Cwd,
		Env:     req.Env,
		Timeout: time.Duration(req.TimeoutSecs) * time.Second,
		Stdin:   req.Stdin,
	}
	var (
		res *core.ExecResult
		err error
	)
	if long {
		res, err = s.svc.Run(r.Context(), r.PathValue("id"), p)
	} else {
		res, err = s.svc.Exec(r.Context(), r.PathValue("id"), p)
	}
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ExecResultDTO{ExitCode: res.ExitCode, Stdout: res.Stdout, Stderr: res.Stderr})
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req ExecRequestDTO
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	jobID, err := s.svc.Launch(r.Context(), r.PathValue("id"), core.ExecParams{
		Cmd:     req.Cmd,
		Cwd:     req.Cwd,
		Env:     req.Env,
		Timeout: time.Duration(req.TimeoutSecs) * time.Second,
		Stdin:   req.Stdin,
	})
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, LaunchResponse{Job: jobID, State: core.JobRunning})
}

func (s *Server) handlePoll(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.Poll(r.PathValue("id"), r.PathValue("job"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "poll_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, JobStatusDTO{
		State:    st.State,
		ExitCode: st.ExitCode,
		Stdout:   st.Stdout,
		Stderr:   st.Stderr,
	})
}

func (s *Server) handleReadFile(w http.ResponseWriter, r *http.Request) {
	var req FileReadRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	b, err := s.svc.ReadFile(r.Context(), r.PathValue("id"), req.Path, req.Cwd)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FileReadResponse{ContentB64: base64.StdEncoding.EncodeToString(b)})
}

func (s *Server) handleWriteFile(w http.ResponseWriter, r *http.Request) {
	var req FileWriteRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	content, err := base64.StdEncoding.DecodeString(req.ContentB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_base64", err.Error())
		return
	}
	if err := s.svc.WriteFile(r.Context(), r.PathValue("id"), req.Path, req.Cwd, content); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func toInstanceDTO(i *core.Instance) *InstanceDTO {
	return &InstanceDTO{
		ID:            i.ID,
		Image:         i.Image,
		State:         i.State,
		Workdir:       i.Workdir,
		UptimeSeconds: i.UptimeSeconds,
		Labels:        i.Labels,
		CreatedAt:     rfc3339(i.CreatedAt),
		UpdatedAt:     rfc3339(i.UpdatedAt),
	}
}

// rfc3339 formats a time as RFC3339, or "" for the zero value.
func rfc3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func notFoundOr(w http.ResponseWriter, err error) {
	if errors.Is(err, core.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, "error", err.Error())
}
