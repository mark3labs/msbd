package api

import "encoding/json"

// dto.go — the JSON wire contract for the msbd REST API.
//
// These shapes are the public interface every client (generated or hand-written)
// depends on. Keep them in lockstep with openapi.yaml. Renaming or removing a
// field is a breaking change — add a new field and deprecate instead.

// ResourcesDTO sizes a sandbox. Zero means "provider default".
type ResourcesDTO struct {
	CPU      float64 `json:"cpu"`
	MemoryMB int     `json:"memory_mb"`
	DiskGB   int     `json:"disk_gb"`
}

// CreateRequest is the body for POST /v1/sandboxes.
type CreateRequest struct {
	Image         string            `json:"image"`
	Resources     ResourcesDTO      `json:"resources"`
	AutoStopSecs  int               `json:"auto_stop_secs"`
	Env           map[string]string `json:"env"`
	Labels        map[string]string `json:"labels"`
	Workdir       string            `json:"workdir"`
	User          string            `json:"user"`
	Hostname      string            `json:"hostname"`
	NetworkPolicy string            `json:"network_policy"`
	Ports         []PortMappingDTO  `json:"ports"`
	Secrets       []SecretDTO       `json:"secrets"`
	Mounts        []MountDTO        `json:"mounts"`
}

// PortMappingDTO forwards a host port to a guest port.
type PortMappingDTO struct {
	HostPort  int    `json:"host_port"`
	GuestPort int    `json:"guest_port"`
	Protocol  string `json:"protocol"`
}

// SecretDTO injects a secret value as a guest env var.
type SecretDTO struct {
	EnvVar string `json:"env_var"`
	Value  string `json:"value"`
}

// MountDTO mounts a named volume at a guest path.
type MountDTO struct {
	GuestPath string `json:"guest_path"`
	Volume    string `json:"volume"`
	Readonly  bool   `json:"readonly"`
}

// InstanceDTO is the canonical sandbox resource returned by every lifecycle
// endpoint.
type InstanceDTO struct {
	ID            string            `json:"id"`
	Image         string            `json:"image"`
	State         string            `json:"state"`
	Workdir       string            `json:"workdir"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	Labels        map[string]string `json:"labels"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

// InspectDTO is the full config + metadata for one sandbox.
type InspectDTO struct {
	InstanceDTO
	Config json.RawMessage `json:"config"`
}

// ExecRequestDTO is the body for exec/run/jobs. TimeoutSecs == 0 means no cap.
type ExecRequestDTO struct {
	Cmd         string            `json:"cmd"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	TimeoutSecs int               `json:"timeout_secs"`
	Stdin       bool              `json:"stdin"`
}

// ExecResultDTO is the result of a synchronous exec/run.
type ExecResultDTO struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// LaunchResponse is returned by POST .../jobs.
type LaunchResponse struct {
	Job   string `json:"job"`
	State string `json:"state"`
}

// JobStatusDTO is the poll result for an async job. State ∈ running|done|dead|gone.
type JobStatusDTO struct {
	State    string `json:"state"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// FileReadRequest / FileReadResponse — base64 keeps the body binary-safe.
type FileReadRequest struct {
	Path string `json:"path"`
	Cwd  string `json:"cwd"`
}
type FileReadResponse struct {
	ContentB64 string `json:"content_b64"`
}

// FileWriteRequest is the body for writing a file.
type FileWriteRequest struct {
	Path       string `json:"path"`
	Cwd        string `json:"cwd"`
	ContentB64 string `json:"content_b64"`
}

// VersionDTO reports the configured default image and the runtime/SDK versions
// for diagnostics.
type VersionDTO struct {
	DefaultImage   string `json:"default_image"`
	RuntimeVersion string `json:"runtime_version"`
	SDKVersion     string `json:"sdk_version"`
}

// ---------------------------------------------------------------------------
// Filesystem (extended)
// ---------------------------------------------------------------------------

// FilePathRequest is the shared input for stat/exists/mkdir/list.
type FilePathRequest struct {
	Path string `json:"path"`
	Cwd  string `json:"cwd"`
}

// FileRemoveRequest removes a file or (recursively) a directory.
type FileRemoveRequest struct {
	Path      string `json:"path"`
	Cwd       string `json:"cwd"`
	Recursive bool   `json:"recursive"`
}

// FileCopyRequest copies/renames within a sandbox.
type FileCopyRequest struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
	Cwd string `json:"cwd"`
}

// HostCopyRequest copies between an allowlisted host path and the sandbox.
type HostCopyRequest struct {
	HostPath  string `json:"host_path"`
	GuestPath string `json:"guest_path"`
	Cwd       string `json:"cwd"`
}

// FileEntryDTO is one directory listing entry.
type FileEntryDTO struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
	Mode uint32 `json:"mode"`
}

// FileListResponse is the result of a directory listing.
type FileListResponse struct {
	Entries []FileEntryDTO `json:"entries"`
}

// FileStatResponse is path metadata.
type FileStatResponse struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    uint32 `json:"mode"`
	ModTime string `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
}

// FileExistsResponse reports whether a path exists.
type FileExistsResponse struct {
	Exists bool `json:"exists"`
}

// ---------------------------------------------------------------------------
// Job control (extended)
// ---------------------------------------------------------------------------

// JobStdinRequest writes data to a running job's stdin.
type JobStdinRequest struct {
	Data       string `json:"data"`        // UTF-8 text
	DataB64    string `json:"data_b64"`    // base64 bytes (takes precedence)
	CloseAfter bool   `json:"close_after"` // close stdin (EOF) after writing
}

// JobSignalRequest sends a signal to a running job. Signal <= 0 means kill.
type JobSignalRequest struct {
	Signal int `json:"signal"`
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

// MetricsDTO is a point-in-time resource snapshot.
type MetricsDTO struct {
	ID                      string  `json:"id"`
	CPUPercent              float64 `json:"cpu_percent"`
	MemoryBytes             uint64  `json:"memory_bytes"`
	MemoryAvailableBytes    *uint64 `json:"memory_available_bytes"`
	MemoryHostResidentBytes *uint64 `json:"memory_host_resident_bytes"`
	MemoryLimitBytes        uint64  `json:"memory_limit_bytes"`
	DiskReadBytes           uint64  `json:"disk_read_bytes"`
	DiskWriteBytes          uint64  `json:"disk_write_bytes"`
	NetRxBytes              uint64  `json:"net_rx_bytes"`
	NetTxBytes              uint64  `json:"net_tx_bytes"`
	UpperUsedBytes          *uint64 `json:"upper_used_bytes"`
	UpperFreeBytes          *uint64 `json:"upper_free_bytes"`
	UptimeSeconds           float64 `json:"uptime_seconds"`
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

// LogEntryDTO is one persisted log line.
type LogEntryDTO struct {
	Source    string `json:"source"`
	Timestamp string `json:"timestamp"`
	Text      string `json:"text"`
	Cursor    string `json:"cursor"`
}

// LogsResponse is a page of persisted logs.
type LogsResponse struct {
	Entries []LogEntryDTO `json:"entries"`
}

// ---------------------------------------------------------------------------
// Volumes
// ---------------------------------------------------------------------------

// VolumeCreateRequest creates a named volume.
type VolumeCreateRequest struct {
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	SizeMiB  int               `json:"size_mb"`
	QuotaMiB int               `json:"quota_mb"`
	Labels   map[string]string `json:"labels"`
}

// VolumeDTO is a named volume summary.
type VolumeDTO struct {
	Name          string            `json:"name"`
	Path          string            `json:"path"`
	Kind          string            `json:"kind"`
	QuotaMiB      *uint32           `json:"quota_mb"`
	UsedBytes     uint64            `json:"used_bytes"`
	CapacityBytes *uint64           `json:"capacity_bytes"`
	DiskFormat    *string           `json:"disk_format"`
	DiskFstype    *string           `json:"disk_fstype"`
	Labels        map[string]string `json:"labels"`
	CreatedAt     string            `json:"created_at"`
}

// VolumeFileRequest is the shared input for volume FS ops.
type VolumeFileRequest struct {
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
	Recursive  bool   `json:"recursive"`
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

// ImagePullRequest asks the daemon to fetch an OCI image into the local cache.
// force=true re-pulls even when the image is already cached.
type ImagePullRequest struct {
	Reference string `json:"reference"`
	Force     bool   `json:"force"`
}

// ImageDTO is a cached-image summary.
type ImageDTO struct {
	Reference      string `json:"reference"`
	ManifestDigest string `json:"manifest_digest"`
	Architecture   string `json:"architecture"`
	OS             string `json:"os"`
	LayerCount     uint   `json:"layer_count"`
	SizeBytes      *int64 `json:"size_bytes"`
	CreatedAt      string `json:"created_at"`
	LastUsedAt     string `json:"last_used_at"`
}

// ImagePruneResponse summarizes a prune.
type ImagePruneResponse struct {
	ImageRefsRemoved uint32  `json:"image_refs_removed"`
	ManifestsRemoved uint32  `json:"manifests_removed"`
	LayersRemoved    uint32  `json:"layers_removed"`
	FsmetaRemoved    uint32  `json:"fsmeta_removed"`
	VMDKRemoved      uint32  `json:"vmdk_removed"`
	BytesReclaimed   *uint64 `json:"bytes_reclaimed"`
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

// SnapshotCreateRequest creates a snapshot from a stopped sandbox.
type SnapshotCreateRequest struct {
	SourceSandbox   string            `json:"source_sandbox"`
	Name            string            `json:"name"`
	Path            string            `json:"path"`
	Labels          map[string]string `json:"labels"`
	Force           bool              `json:"force"`
	RecordIntegrity bool              `json:"record_integrity"`
}

// SnapshotDTO is a snapshot summary.
type SnapshotDTO struct {
	Digest       string  `json:"digest"`
	Name         *string `json:"name"`
	ParentDigest *string `json:"parent_digest"`
	ImageRef     string  `json:"image_ref"`
	Format       string  `json:"format"`
	SizeBytes    *uint64 `json:"size_bytes"`
	Path         string  `json:"path"`
	CreatedAt    string  `json:"created_at"`
}

// SnapshotExportRequest exports a snapshot to an allowlisted host archive.
type SnapshotExportRequest struct {
	NameOrPath  string `json:"name_or_path"`
	OutPath     string `json:"out_path"`
	WithParents bool   `json:"with_parents"`
	WithImage   bool   `json:"with_image"`
	PlainTar    bool   `json:"plain_tar"`
}

// SnapshotImportRequest imports a snapshot archive from an allowlisted host path.
type SnapshotImportRequest struct {
	Archive string `json:"archive"`
	Dest    string `json:"dest"`
}

// SnapshotReindexRequest rebuilds the local snapshot index.
type SnapshotReindexRequest struct {
	Dir string `json:"dir"`
}

// SnapshotReindexResponse reports how many snapshots were indexed.
type SnapshotReindexResponse struct {
	Indexed uint32 `json:"indexed"`
}

// SnapshotVerifyResponse is the result of an integrity check.
type SnapshotVerifyResponse struct {
	Digest      string `json:"digest"`
	Path        string `json:"path"`
	UpperKind   string `json:"upper_kind"`
	UpperAlgo   string `json:"upper_algorithm"`
	UpperDigest string `json:"upper_digest"`
}

// ErrorBody is the uniform error envelope: {"error":{"code","message"}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
