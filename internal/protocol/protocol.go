package protocol

import "time"

const (
	SchemaVersion        = 3
	DefaultBootstrapURL  = "https://46.120.103.61:8777"
	DefaultListen        = "0.0.0.0:8777"
	DefaultPort          = 8777
	EnrollmentTTL        = 10 * time.Minute
	ReportInterval       = 5 * time.Second
	CheckInterval        = 5 * time.Second
	DiscoveryInterval    = 60 * time.Second
	DiskInterval         = 30 * time.Second
	InventoryInterval    = 5 * time.Minute
	PingInterval         = 5 * time.Second
	PingTimeout          = time.Second
	PingWindow           = 60 * time.Second
	HTTPProbeTimeout     = 2 * time.Second
	ICMPTimeout          = time.Second
	MaxConcurrentProbes  = 16
	DiscoveryBudgetMin   = 128
	RetryMax             = 60 * time.Second
	SpoolMaxAge          = 30 * time.Minute
	SpoolMaxBytes        = 100 * 1024 * 1024
	StaleContact         = 15 * time.Second
	UnreachableContact   = 30 * time.Second
	CheckFailCount       = 3
	CheckRecoverCount    = 2
	OneShotExpiry        = 10 * time.Minute
	ConfigOverdue        = 24 * time.Hour
	RawRetention         = 48 * time.Hour
	MinuteRetention      = 30 * 24 * time.Hour
	CoarseRetention      = 180 * 24 * time.Hour
	LocalProbation       = 120 * time.Second
	RemoteConfirmWait    = 300 * time.Second
	ActivationWindow     = 24 * time.Hour
	CanaryObserve        = 5 * time.Minute
	MaxConcurrentUpdates = 5
)

type CapabilityStatus string

const (
	CapSupported        CapabilityStatus = "supported"
	CapPartial          CapabilityStatus = "partial"
	CapUnsupported      CapabilityStatus = "unsupported"
	CapPermissionDenied CapabilityStatus = "permission_denied"
	CapError            CapabilityStatus = "error"
)

type ObservationQuality string

const (
	QualityOK      ObservationQuality = "ok"
	QualityLate    ObservationQuality = "late"
	QualityMissed  ObservationQuality = "missed"
	QualitySkipped ObservationQuality = "skipped"
	QualityStale   ObservationQuality = "stale"
	QualityUnknown ObservationQuality = "unknown"
	QualityPaused  ObservationQuality = "paused"
	QualityError   ObservationQuality = "error"
)

type OperationStatus string

const (
	OpQueued            OperationStatus = "queued"
	OpRunning           OperationStatus = "running"
	OpAttentionRequired OperationStatus = "attention_required"
	OpCompleted         OperationStatus = "completed"
	OpCompletedWithErrs OperationStatus = "completed_with_errors"
	OpCancelled         OperationStatus = "cancelled"
)

type TargetStatus string

const (
	TargetQueued               TargetStatus = "queued"
	TargetWaitingOffline       TargetStatus = "waiting_offline"
	TargetAccepted             TargetStatus = "accepted"
	TargetRunning              TargetStatus = "running"
	TargetAwaitingConfirmation TargetStatus = "awaiting_confirmation"
	TargetSucceeded            TargetStatus = "succeeded"
	TargetRejected             TargetStatus = "rejected"
	TargetFailed               TargetStatus = "failed"
	TargetRolledBack           TargetStatus = "rolled_back"
	TargetUnsupported          TargetStatus = "unsupported"
	TargetExpired              TargetStatus = "expired"
	TargetCancelledBeforeExec  TargetStatus = "cancelled_before_execution"
	TargetUnknownResult        TargetStatus = "unknown_result"
)

type AgentReport struct {
	SchemaVersion      int                   `json:"schema_version"`
	AgentID            string                `json:"agent_id"`
	SessionID          string                `json:"session_id"`
	Sequence           int64                 `json:"sequence"`
	ObservedAt         time.Time             `json:"observed_at"`
	ReportedAt         time.Time             `json:"reported_at"`
	ConfigRevision     int64                 `json:"config_revision"`
	ConfigHash         string                `json:"config_hash"`
	EndpointGeneration int64                 `json:"endpoint_generation"`
	WorkerVersion      string                `json:"worker_version"`
	WorkerDigest       string                `json:"worker_digest,omitempty"`
	ServiceHostVersion string                `json:"service_host_version,omitempty"`
	ServiceHostDigest  string                `json:"service_host_digest,omitempty"`
	ManagedReady       bool                  `json:"managed_ready"`
	Capabilities       map[string]Capability `json:"capabilities"`
	Host               *HostMetrics          `json:"host,omitempty"`
	Checks             []CheckObservation    `json:"checks,omitempty"`
	Discovery          *DiscoveryDelta       `json:"discovery,omitempty"`
	JobReceipts        []JobReceipt          `json:"job_receipts,omitempty"`
	Spool              *SpoolStatus          `json:"spool,omitempty"`
	IsLive             bool                  `json:"is_live"`
	ControlCursor      string                `json:"control_cursor,omitempty"`
	UpdateTx           *UpdateTxStatus       `json:"update_tx,omitempty"`
	Migration          *MigrationStatus      `json:"migration,omitempty"`
}

type Capability struct {
	Status           CapabilityStatus `json:"status"`
	Reason           string           `json:"reason,omitempty"`
	LastSuccess      *time.Time       `json:"last_success,omitempty"`
	RestartSupported bool             `json:"restart_supported,omitempty"`
	UpdateSupported  bool             `json:"update_supported,omitempty"`
}

type HostMetrics struct {
	Hostname     string        `json:"hostname"`
	DisplayName  string        `json:"display_name,omitempty"`
	OS           string        `json:"os"`
	OSVersion    string        `json:"os_version"`
	Arch         string        `json:"arch"`
	Addresses    []string      `json:"addresses"`
	SystemUptime float64       `json:"system_uptime_seconds"`
	AgentUptime  float64       `json:"agent_uptime_seconds"`
	CPUModel     string        `json:"cpu_model,omitempty"`
	CPULogical   int           `json:"cpu_logical"`
	CPUPercent   *float64      `json:"cpu_percent"`
	RAMTotal     int64         `json:"ram_total_bytes"`
	RAMUsed      int64         `json:"ram_used_bytes"`
	RAMAvailable int64         `json:"ram_available_bytes"`
	Temperatures []Temperature `json:"temperatures,omitempty"`
	Disks        []Disk        `json:"disks,omitempty"`
	Ping         *PingSummary  `json:"ping,omitempty"`
}

type Temperature struct {
	Source   string             `json:"source"`
	Label    string             `json:"label"`
	Celsius  *float64           `json:"celsius"`
	Observed time.Time          `json:"observed_at"`
	Quality  ObservationQuality `json:"quality"`
	Reason   string             `json:"reason,omitempty"`
}

type Disk struct {
	Mount      string  `json:"mount"`
	FS         string  `json:"fs"`
	Total      int64   `json:"total_bytes"`
	Used       int64   `json:"used_bytes"`
	Available  int64   `json:"available_bytes"`
	UsedPct    float64 `json:"used_percent"`
	InodesTot  *int64  `json:"inodes_total,omitempty"`
	InodesUsed *int64  `json:"inodes_used,omitempty"`
}

type PingSummary struct {
	Target     string   `json:"target"`
	MeanMS     *float64 `json:"mean_ms"`
	MinMS      *float64 `json:"min_ms"`
	MaxMS      *float64 `json:"max_ms"`
	Sent       int      `json:"sent"`
	Received   int      `json:"received"`
	LossPct    *float64 `json:"loss_percent"`
	Permission string   `json:"permission,omitempty"`
	WindowSec  int      `json:"window_seconds"`
}

type CheckObservation struct {
	ServiceID  string             `json:"service_id"`
	CheckID    string             `json:"check_id"`
	ObservedAt time.Time          `json:"observed_at"`
	Vantage    string             `json:"vantage"`
	DialTarget string             `json:"dial_target"`
	URL        string             `json:"url"`
	Transport  string             `json:"transport"` // ok, timeout, refused, tls_error, blocked
	TLSValid   *bool              `json:"tls_valid,omitempty"`
	TLSReason  string             `json:"tls_reason,omitempty"`
	HTTPStatus *int               `json:"http_status,omitempty"`
	LatencyMS  *float64           `json:"latency_ms,omitempty"`
	AppResult  string             `json:"app_result,omitempty"` // not_configured, pass, fail
	AppReason  string             `json:"app_reason,omitempty"`
	Quality    ObservationQuality `json:"quality"`
	ConfigRev  int64              `json:"config_revision"`
}

type DiscoveryDelta struct {
	Kind             string                `json:"kind"` // snapshot or delta
	CoverageComplete bool                  `json:"coverage_complete"`
	ListenerCount    int                   `json:"listener_count"`
	Confirmed        []DiscoveredEndpoint  `json:"confirmed,omitempty"`
	Unresolved       []UnresolvedCandidate `json:"unresolved,omitempty"`
	Truncated        bool                  `json:"truncated"`
	PermissionGaps   []string              `json:"permission_gaps,omitempty"`
	StartedAt        time.Time             `json:"started_at"`
	EndedAt          time.Time             `json:"ended_at"`
	JobID            string                `json:"job_id,omitempty"`
}

type DiscoveredEndpoint struct {
	ServiceID     string    `json:"service_id"`
	DialTarget    string    `json:"dial_target"`
	URL           string    `json:"url"`
	HostHeader    string    `json:"host_header,omitempty"`
	TLSServerName string    `json:"tls_server_name,omitempty"`
	SpeaksHTTP    bool      `json:"speaks_http"`
	SpeaksTLS     bool      `json:"speaks_tls"`
	ProcessName   string    `json:"process_name,omitempty"`
	PID           int       `json:"pid,omitempty"`
	Source        string    `json:"source"`
	FirstSeen     time.Time `json:"first_seen"`
}

type UnresolvedCandidate struct {
	DialTarget  string `json:"dial_target"`
	Reason      string `json:"reason"`
	ProcessName string `json:"process_name,omitempty"`
}

type JobReceipt struct {
	JobID       string         `json:"job_id"`
	OperationID string         `json:"operation_id"`
	Status      TargetStatus   `json:"status"`
	Stage       string         `json:"stage"`
	Message     string         `json:"message"`
	ErrorCode   string         `json:"error_code,omitempty"`
	Retryable   bool           `json:"retryable"`
	Evidence    map[string]any `json:"evidence,omitempty"`
	AcceptedAt  *time.Time     `json:"accepted_at,omitempty"`
	AppliedAt   *time.Time     `json:"applied_at,omitempty"`
	VerifiedAt  *time.Time     `json:"verified_at,omitempty"`
}

type SpoolStatus struct {
	Bytes    int64      `json:"bytes"`
	Items    int        `json:"items"`
	Dropped  int64      `json:"dropped"`
	DropFrom *time.Time `json:"drop_from,omitempty"`
	DropTo   *time.Time `json:"drop_to,omitempty"`
}

type UpdateTxStatus struct {
	TxID      string `json:"tx_id"`
	Stage     string `json:"stage"`
	OldDigest string `json:"old_digest,omitempty"`
	NewDigest string `json:"new_digest,omitempty"`
}

type MigrationStatus struct {
	PlanID     string `json:"plan_id"`
	State      string `json:"state"`
	Generation int64  `json:"generation"`
	Candidate  string `json:"candidate,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type ControlResponse struct {
	ReceiptAcks   []string       `json:"receipt_acks,omitempty"`
	Ack           *IngestAck     `json:"ack,omitempty"`
	DesiredConfig *DesiredConfig `json:"desired_config,omitempty"`
	Jobs          []JobEnvelope  `json:"jobs,omitempty"`
	Migration     *MigrationPlan `json:"migration,omitempty"`
	Cursor        string         `json:"cursor,omitempty"`
	ControllerID  string         `json:"controller_id"`
	ServerTime    time.Time      `json:"server_time"`
	Error         string         `json:"error,omitempty"`
}

type IngestAck struct {
	UpToSequence int64 `json:"up_to_sequence"`
	Committed    bool  `json:"committed"`
}

type DesiredConfig struct {
	Revision int64       `json:"revision"`
	Hash     string      `json:"hash"`
	Body     AgentConfig `json:"body"`
}

type AgentConfig struct {
	DisplayName  string            `json:"display_name,omitempty"`
	Intervals    IntervalConfig    `json:"intervals"`
	Collectors   CollectorConfig   `json:"collectors"`
	Ping         PingConfig        `json:"ping"`
	Checks       []CheckDefinition `json:"checks"`
	Paused       bool              `json:"paused"`
	SelectedTemp string            `json:"selected_temperature,omitempty"`
}

type IntervalConfig struct {
	CollectSeconds   int `json:"collect_seconds"`
	ReportSeconds    int `json:"report_seconds"`
	CheckSeconds     int `json:"check_seconds"`
	DiscoverySeconds int `json:"discovery_seconds"`
	DiskSeconds      int `json:"disk_seconds"`
}

type CollectorConfig struct {
	CPU          bool `json:"cpu"`
	Memory       bool `json:"memory"`
	Temperatures bool `json:"temperatures"`
	Disks        bool `json:"disks"`
	Ping         bool `json:"ping"`
}

type PingConfig struct {
	Enabled         bool   `json:"enabled"`
	Target          string `json:"target"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	WindowSeconds   int    `json:"window_seconds"`
}

type CheckDefinition struct {
	ID              string `json:"id"`
	ServiceID       string `json:"service_id"`
	Kind            string `json:"kind"` // baseline_http, configured_http, server_remote
	URL             string `json:"url"`
	DialTarget      string `json:"dial_target,omitempty"`
	Method          string `json:"method"`
	HostHeader      string `json:"host_header,omitempty"`
	TLSServerName   string `json:"tls_server_name,omitempty"`
	Path            string `json:"path,omitempty"`
	ExpectedStatus  []int  `json:"expected_status,omitempty"`
	ExpectText      string `json:"expect_text,omitempty"`
	ExpectJSONPath  string `json:"expect_json_path,omitempty"`
	ExpectJSONValue string `json:"expect_json_value,omitempty"`
	LatencyMS       *int   `json:"latency_ms,omitempty"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	IntervalSeconds int    `json:"interval_seconds"`
	SecretID        string `json:"secret_id,omitempty"`
	SecretHeader    string `json:"secret_header,omitempty"`
	Paused          bool   `json:"paused"`
	Ignored         bool   `json:"ignored"`
	InsecureTLS     bool   `json:"insecure_tls"`
}

type JobEnvelope struct {
	JobID            string         `json:"job_id"`
	OperationID      string         `json:"operation_id"`
	IdempotencyKey   string         `json:"idempotency_key"`
	Action           string         `json:"action"`
	SchemaVersion    int            `json:"schema_version"`
	ControllerID     string         `json:"controller_id"`
	ActorID          string         `json:"actor_id"`
	ExpectedRevision *int64         `json:"expected_revision,omitempty"`
	Params           map[string]any `json:"params"`
	CreatedAt        time.Time      `json:"created_at"`
	NotBefore        time.Time      `json:"not_before"`
	Deadline         time.Time      `json:"deadline"`
	Risk             string         `json:"risk"`
}

type MigrationPlan struct {
	PlanID             string    `json:"plan_id"`
	ControllerID       string    `json:"controller_id"`
	CurrentURL         string    `json:"current_url"`
	CandidateURL       string    `json:"candidate_url"`
	Generation         int64     `json:"generation"`
	Mode               string    `json:"mode"`
	PayloadHash        string    `json:"payload_hash"`
	TrustPEM           string    `json:"trust_pem,omitempty"`
	ExpiresAt          time.Time `json:"expires_at"`
	ArmFallback        bool      `json:"arm_fallback"`
	PrimaryLossSeconds int       `json:"primary_loss_seconds"`
}

type EnrollRequest struct {
	Code        string `json:"code"`
	DisplayName string `json:"display_name,omitempty"`
	Hostname    string `json:"hostname"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	AgentID     string `json:"agent_id"`
	Version     string `json:"version"`
}

type EnrollResponse struct {
	AgentID            string `json:"agent_id"`
	Credential         string `json:"credential"`
	ControllerID       string `json:"controller_id"`
	AdvertisedURL      string `json:"advertised_url"`
	CACertPEM          string `json:"ca_cert_pem"`
	ConfigRevision     int64  `json:"config_revision"`
	EndpointGeneration int64  `json:"endpoint_generation"`
}

type Operation struct {
	ID               string          `json:"operation_id"`
	Action           string          `json:"action"`
	Status           OperationStatus `json:"status"`
	Revision         int64           `json:"revision"`
	ClientRequestKey string          `json:"client_request_key"`
	Actor            string          `json:"actor"`
	CreatedAt        time.Time       `json:"created_at"`
	Deadline         *time.Time      `json:"deadline,omitempty"`
	Summary          string          `json:"summary"`
	Params           map[string]any  `json:"params,omitempty"`
	Targets          []TargetResult  `json:"targets"`
	ParentID         string          `json:"parent_id,omitempty"`
}

type TargetResult struct {
	AgentID   string         `json:"agent_id"`
	Status    TargetStatus   `json:"status"`
	Stage     string         `json:"stage"`
	Message   string         `json:"message"`
	ErrorCode string         `json:"error_code,omitempty"`
	Retryable bool           `json:"retryable"`
	Evidence  map[string]any `json:"evidence,omitempty"`
}

type SubmitOperation struct {
	Action           string         `json:"action"`
	ClientRequestKey string         `json:"client_request_key"`
	TargetIDs        []string       `json:"target_ids,omitempty"`
	TargetMode       string         `json:"target_mode,omitempty"` // selected, matching, all
	Params           map[string]any `json:"params"`
	BaseRevision     *int64         `json:"base_revision,omitempty"`
	RecentAuthToken  string         `json:"recent_auth_token,omitempty"`
}

func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		Intervals: IntervalConfig{
			CollectSeconds:   5,
			ReportSeconds:    5,
			CheckSeconds:     5,
			DiscoverySeconds: 60,
			DiskSeconds:      30,
		},
		Collectors: CollectorConfig{CPU: true, Memory: true, Temperatures: true, Disks: true, Ping: true},
		Ping: PingConfig{
			Enabled: true, Target: "8.8.8.8",
			IntervalSeconds: 5, TimeoutSeconds: 1, WindowSeconds: 60,
		},
	}
}
