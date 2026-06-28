package api

// dto.go — the wire contract between shipagent's msbprovider adapter and msbd.
//
// These JSON shapes deliberately mirror the provider-neutral value types in
// shipagent's internal/services/sandbox/provider.go (CreateOpts, ExecRequest,
// Instance, ExecResult, JobStatus, …) so the HTTP adapter is a near-1:1
// translation with no semantic drift.

// ResourcesDTO mirrors sandbox.Resources. Zero means "provider default".
type ResourcesDTO struct {
	CPU      float64 `json:"cpu"`
	MemoryMB int     `json:"memory_mb"`
	DiskGB   int     `json:"disk_gb"`
}

// CreateRequest mirrors sandbox.CreateOpts.
type CreateRequest struct {
	Image        string            `json:"image"`
	Resources    ResourcesDTO      `json:"resources"`
	AutoStopSecs int               `json:"auto_stop_secs"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Workdir      string            `json:"workdir"`
}

// InstanceDTO mirrors sandbox.Instance — the canonical resource returned by
// every lifecycle endpoint.
type InstanceDTO struct {
	ID            string            `json:"id"`
	Image         string            `json:"image"`
	State         string            `json:"state"`
	Workdir       string            `json:"workdir"`
	UptimeSeconds float64           `json:"uptime_seconds"`
	CostUSD       float64           `json:"cost_usd"`
	Labels        map[string]string `json:"labels"`
}

// ExecRequestDTO mirrors sandbox.ExecRequest. TimeoutSecs == 0 means no cap.
type ExecRequestDTO struct {
	Cmd         string            `json:"cmd"`
	Cwd         string            `json:"cwd"`
	Env         map[string]string `json:"env"`
	TimeoutSecs int               `json:"timeout_secs"`
}

// ExecResultDTO mirrors sandbox.ExecResult.
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

// JobStatusDTO mirrors sandbox.JobStatus. State ∈ running|done|dead|gone.
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

// FileWriteRequest mirrors sandbox.FileWrite.
type FileWriteRequest struct {
	Path       string `json:"path"`
	Cwd        string `json:"cwd"`
	ContentB64 string `json:"content_b64"`
}

// CapabilitiesDTO mirrors sandbox.Capabilities plus runtime metadata used for
// diagnostics and the settings UI.
type CapabilitiesDTO struct {
	PrebakedImage  bool   `json:"prebaked_image"`
	NativeFileIO   bool   `json:"native_file_io"`
	NativeSessions bool   `json:"native_sessions"`
	ReportsCost    bool   `json:"reports_cost"`
	DefaultImage   string `json:"default_image"`
	RuntimeVersion string `json:"runtime_version"`
}

// ErrorBody is the uniform error envelope: {"error":{"code","message"}}.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
