package api

// handlers_ext.go — handlers for the extended surface: inspect, metrics, logs,
// filesystem ops, job control, volumes, images, snapshots. Each is a near-1:1
// DTO ⇄ core translation, mirroring the conventions in handlers.go.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/mark3labs/msbd/internal/core"
)

// ---------------------------------------------------------------------------
// Inspect
// ---------------------------------------------------------------------------

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	res, err := s.svc.Inspect(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOr(w, err)
		return
	}
	cfg := json.RawMessage(res.ConfigJSON)
	if res.ConfigJSON == "" {
		cfg = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, InspectDTO{
		InstanceDTO: *toInstanceDTO(&res.Instance),
		Config:      cfg,
	})
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

func toMetricsDTO(m *core.Metrics) MetricsDTO {
	return MetricsDTO{
		ID:                      m.ID,
		CPUPercent:              m.CPUPercent,
		MemoryBytes:             m.MemoryBytes,
		MemoryAvailableBytes:    m.MemoryAvailableBytes,
		MemoryHostResidentBytes: m.MemoryHostResidentBytes,
		MemoryLimitBytes:        m.MemoryLimitBytes,
		DiskReadBytes:           m.DiskReadBytes,
		DiskWriteBytes:          m.DiskWriteBytes,
		NetRxBytes:              m.NetRxBytes,
		NetTxBytes:              m.NetTxBytes,
		UpperUsedBytes:          m.UpperUsedBytes,
		UpperFreeBytes:          m.UpperFreeBytes,
		UptimeSeconds:           m.UptimeSeconds,
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	m, err := s.svc.Metrics(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toMetricsDTO(m))
}

func (s *Server) handleMetricsAll(w http.ResponseWriter, r *http.Request) {
	all, err := s.svc.AllMetrics(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "metrics_failed", err.Error())
		return
	}
	out := make([]MetricsDTO, 0, len(all))
	for i := range all {
		out = append(out, toMetricsDTO(&all[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := core.LogQuery{}
	if tail := r.URL.Query().Get("tail"); tail != "" {
		if n, err := strconv.ParseUint(tail, 10, 64); err == nil {
			q.Tail = n
		}
	}
	if src := r.URL.Query().Get("sources"); src != "" {
		q.Sources = splitCSV(src)
	}
	entries, err := s.svc.Logs(r.Context(), r.PathValue("id"), q)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	out := make([]LogEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, LogEntryDTO{
			Source:    e.Source,
			Timestamp: rfc3339(e.Timestamp),
			Text:      e.Text,
			Cursor:    e.Cursor,
		})
	}
	writeJSON(w, http.StatusOK, LogsResponse{Entries: out})
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Filesystem (extended)
// ---------------------------------------------------------------------------

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	var req FilePathRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	entries, err := s.svc.ListDir(r.Context(), r.PathValue("id"), req.Path, req.Cwd)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	out := make([]FileEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, FileEntryDTO{Path: e.Path, Kind: e.Kind, Size: e.Size, Mode: e.Mode})
	}
	writeJSON(w, http.StatusOK, FileListResponse{Entries: out})
}

func (s *Server) handleFileStat(w http.ResponseWriter, r *http.Request) {
	var req FilePathRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	st, err := s.svc.Stat(r.Context(), r.PathValue("id"), req.Path, req.Cwd)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FileStatResponse{
		Path:    st.Path,
		Size:    st.Size,
		Mode:    st.Mode,
		ModTime: rfc3339(st.ModTime),
		IsDir:   st.IsDir,
	})
}

func (s *Server) handleFileExists(w http.ResponseWriter, r *http.Request) {
	var req FilePathRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ok, err := s.svc.Exists(r.Context(), r.PathValue("id"), req.Path, req.Cwd)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FileExistsResponse{Exists: ok})
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	var req FilePathRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.Mkdir(r.Context(), r.PathValue("id"), req.Path, req.Cwd); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileRemove(w http.ResponseWriter, r *http.Request) {
	var req FileRemoveRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.Remove(r.Context(), r.PathValue("id"), req.Path, req.Cwd, req.Recursive); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileCopy(w http.ResponseWriter, r *http.Request) {
	var req FileCopyRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.Copy(r.Context(), r.PathValue("id"), req.Src, req.Dst, req.Cwd); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileRename(w http.ResponseWriter, r *http.Request) {
	var req FileCopyRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.Rename(r.Context(), r.PathValue("id"), req.Src, req.Dst, req.Cwd); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileCopyFromHost(w http.ResponseWriter, r *http.Request) {
	var req HostCopyRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.CopyFromHost(r.Context(), r.PathValue("id"), req.HostPath, req.GuestPath, req.Cwd); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFileCopyToHost(w http.ResponseWriter, r *http.Request) {
	var req HostCopyRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.CopyToHost(r.Context(), r.PathValue("id"), req.GuestPath, req.HostPath, req.Cwd); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Job control (extended)
// ---------------------------------------------------------------------------

func (s *Server) handleJobStdin(w http.ResponseWriter, r *http.Request) {
	var req JobStdinRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	var data []byte
	if req.DataB64 != "" {
		b, err := base64.StdEncoding.DecodeString(req.DataB64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad_base64", err.Error())
			return
		}
		data = b
	} else {
		data = []byte(req.Data)
	}
	id, job := r.PathValue("id"), r.PathValue("job")
	if len(data) > 0 {
		if err := s.svc.WriteJobStdin(id, job, data); err != nil {
			notFoundOr(w, err)
			return
		}
	}
	if req.CloseAfter {
		if err := s.svc.CloseJobStdin(id, job); err != nil {
			notFoundOr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleJobSignal(w http.ResponseWriter, r *http.Request) {
	var req JobSignalRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.SignalJob(r.Context(), r.PathValue("id"), r.PathValue("job"), req.Signal); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Volumes
// ---------------------------------------------------------------------------

func toVolumeDTO(v *core.Volume) VolumeDTO {
	return VolumeDTO{
		Name:          v.Name,
		Path:          v.Path,
		Kind:          v.Kind,
		QuotaMiB:      v.QuotaMiB,
		UsedBytes:     v.UsedBytes,
		CapacityBytes: v.CapacityBytes,
		DiskFormat:    v.DiskFormat,
		DiskFstype:    v.DiskFstype,
		Labels:        v.Labels,
		CreatedAt:     rfc3339(v.CreatedAt),
	}
}

func (s *Server) handleVolumeCreate(w http.ResponseWriter, r *http.Request) {
	var req VolumeCreateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v, err := s.svc.CreateVolume(r.Context(), core.VolumeParams{
		Name:     req.Name,
		Kind:     req.Kind,
		SizeMiB:  req.SizeMiB,
		QuotaMiB: req.QuotaMiB,
		Labels:   req.Labels,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toVolumeDTO(v))
}

func (s *Server) handleVolumeList(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListVolumes(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]VolumeDTO, 0, len(list))
	for i := range list {
		out = append(out, toVolumeDTO(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleVolumeGet(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.GetVolume(r.Context(), r.PathValue("name"))
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toVolumeDTO(v))
}

func (s *Server) handleVolumeDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.RemoveVolume(r.Context(), r.PathValue("name")); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVolumeReadFile(w http.ResponseWriter, r *http.Request) {
	var req VolumeFileRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	b, err := s.svc.VolumeReadFile(r.Context(), r.PathValue("name"), req.Path)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FileReadResponse{ContentB64: base64.StdEncoding.EncodeToString(b)})
}

func (s *Server) handleVolumeWriteFile(w http.ResponseWriter, r *http.Request) {
	var req VolumeFileRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	content, err := base64.StdEncoding.DecodeString(req.ContentB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_base64", err.Error())
		return
	}
	if err := s.svc.VolumeWriteFile(r.Context(), r.PathValue("name"), req.Path, content); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVolumeMkdir(w http.ResponseWriter, r *http.Request) {
	var req VolumeFileRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.VolumeMkdir(r.Context(), r.PathValue("name"), req.Path); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVolumeRemoveFile(w http.ResponseWriter, r *http.Request) {
	var req VolumeFileRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.VolumeRemove(r.Context(), r.PathValue("name"), req.Path, req.Recursive); err != nil {
		notFoundOr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVolumeExists(w http.ResponseWriter, r *http.Request) {
	var req VolumeFileRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ok, err := s.svc.VolumeExists(r.Context(), r.PathValue("name"), req.Path)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, FileExistsResponse{Exists: ok})
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

func toImageDTO(i *core.Image) ImageDTO {
	return ImageDTO{
		Reference:      i.Reference,
		ManifestDigest: i.ManifestDigest,
		Architecture:   i.Architecture,
		OS:             i.OS,
		LayerCount:     i.LayerCount,
		SizeBytes:      i.SizeBytes,
		CreatedAt:      rfc3339(i.CreatedAt),
		LastUsedAt:     rfc3339(i.LastUsedAt),
	}
}

func (s *Server) handleImageList(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListImages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]ImageDTO, 0, len(list))
	for i := range list {
		out = append(out, toImageDTO(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleImageInspect(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("reference")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "reference query param required")
		return
	}
	d, err := s.svc.InspectImage(r.Context(), ref)
	if err != nil {
		notFoundOr(w, err)
		return
	}
	// Marshal the detail directly: Image fields + config/layers carry their own tags.
	writeJSON(w, http.StatusOK, struct {
		ImageDTO
		Config *core.ImageConfig `json:"config"`
		Layers []core.ImageLayer `json:"layers"`
	}{
		ImageDTO: toImageDTO(&d.Image),
		Config:   d.Config,
		Layers:   d.Layers,
	})
}

func (s *Server) handleImageRemove(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("reference")
	if ref == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "reference query param required")
		return
	}
	force := r.URL.Query().Get("force") == "true"
	if err := s.svc.RemoveImage(r.Context(), ref, force); err != nil {
		writeErr(w, http.StatusInternalServerError, "remove_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleImagePull fetches an OCI image into the local cache. It boots a
// throwaway microVM to trigger the pull (the SDK has no standalone pull), so it
// is a long-running, blocking call — do not front it with a low-timeout proxy.
func (s *Server) handleImagePull(w http.ResponseWriter, r *http.Request) {
	var req ImagePullRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if strings.TrimSpace(req.Reference) == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "reference is required")
		return
	}
	img, err := s.svc.PullImage(r.Context(), req.Reference, req.Force)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "pull_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toImageDTO(img))
}

func (s *Server) handleImagePrune(w http.ResponseWriter, r *http.Request) {
	rep, err := s.svc.PruneImages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "prune_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ImagePruneResponse{
		ImageRefsRemoved: rep.ImageRefsRemoved,
		ManifestsRemoved: rep.ManifestsRemoved,
		LayersRemoved:    rep.LayersRemoved,
		FsmetaRemoved:    rep.FsmetaRemoved,
		VMDKRemoved:      rep.VMDKRemoved,
		BytesReclaimed:   rep.BytesReclaimed,
	})
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

func toSnapshotDTO(s *core.Snapshot) SnapshotDTO {
	return SnapshotDTO{
		Digest:       s.Digest,
		Name:         s.Name,
		ParentDigest: s.ParentDigest,
		ImageRef:     s.ImageRef,
		Format:       s.Format,
		SizeBytes:    s.SizeBytes,
		Path:         s.Path,
		CreatedAt:    rfc3339(s.CreatedAt),
	}
}

func (s *Server) handleSnapshotCreate(w http.ResponseWriter, r *http.Request) {
	var req SnapshotCreateRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	snap, err := s.svc.CreateSnapshot(r.Context(), core.SnapshotCreateParams{
		SourceSandbox:   req.SourceSandbox,
		Name:            req.Name,
		Path:            req.Path,
		Labels:          req.Labels,
		Force:           req.Force,
		RecordIntegrity: req.RecordIntegrity,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toSnapshotDTO(snap))
}

func (s *Server) handleSnapshotList(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListSnapshots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "list_failed", err.Error())
		return
	}
	out := make([]SnapshotDTO, 0, len(list))
	for i := range list {
		out = append(out, toSnapshotDTO(&list[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSnapshotGet(w http.ResponseWriter, r *http.Request) {
	snap, err := s.svc.GetSnapshot(r.Context(), r.PathValue("name"))
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSnapshotDTO(snap))
}

func (s *Server) handleSnapshotDelete(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	if err := s.svc.RemoveSnapshot(r.Context(), r.PathValue("name"), force); err != nil {
		writeErr(w, http.StatusInternalServerError, "remove_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSnapshotVerify(w http.ResponseWriter, r *http.Request) {
	v, err := s.svc.VerifySnapshot(r.Context(), r.PathValue("name"))
	if err != nil {
		notFoundOr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, SnapshotVerifyResponse{
		Digest:      v.Digest,
		Path:        v.Path,
		UpperKind:   v.UpperKind,
		UpperAlgo:   v.UpperAlgo,
		UpperDigest: v.UpperDigest,
	})
}

func (s *Server) handleSnapshotExport(w http.ResponseWriter, r *http.Request) {
	var req SnapshotExportRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.svc.ExportSnapshot(r.Context(), req.NameOrPath, req.OutPath, req.WithParents, req.WithImage, req.PlainTar); err != nil {
		writeErr(w, http.StatusInternalServerError, "export_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSnapshotImport(w http.ResponseWriter, r *http.Request) {
	var req SnapshotImportRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	snap, err := s.svc.ImportSnapshot(r.Context(), req.Archive, req.Dest)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "import_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toSnapshotDTO(snap))
}

func (s *Server) handleSnapshotReindex(w http.ResponseWriter, r *http.Request) {
	var req SnapshotReindexRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	n, err := s.svc.ReindexSnapshots(r.Context(), req.Dir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "reindex_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, SnapshotReindexResponse{Indexed: n})
}
