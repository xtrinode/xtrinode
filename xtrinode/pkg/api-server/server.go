package apiserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// ServerConfig holds configuration for the API server
type ServerConfig struct {
	Port                 int
	APIPath              string        // Base path for API endpoints (e.g., "/api/v1")
	HealthPath           string        // Health check path (e.g., "/health")
	MetricsPath          string        // Metrics path (e.g., "/metrics")
	ReadTimeout          time.Duration // HTTP read timeout
	WriteTimeout         time.Duration // HTTP write timeout
	ShutdownTimeout      time.Duration // Graceful shutdown timeout
	RequestTimeout       time.Duration // Per-request timeout for K8s operations
	ResumeLeaseDuration  time.Duration // Lease duration for resume operations (prevents stampede)
	SuspendLeaseDuration time.Duration // Lease duration for suspend operations (prevents stampede)
	RetryAfterSeconds    int           // Retry-After header value for 202 responses
	LeaseNamespace       string        // Namespace for K8s Lease objects
	LeaseHolderIdentity  string        // Identity for K8s Lease holder (default: xtrinode-api-server)
	AuthEnabled          bool          // Require bearer auth for API endpoints
	AuthToken            string        // Admin bearer token with all API actions
	ResumeAuthToken      string        // Optional resume-only bearer token for Gateway -> API Server calls
	CORSAllowedOrigins   []string      // Optional browser origins allowed to call API endpoints
}

// Server provides REST API endpoints for XTrinode management
type Server struct {
	client       client.Client
	log          logr.Logger
	server       *http.Server
	config       ServerConfig
	leaseManager *LeaseManager
}

// NewServer creates a new REST API server
func NewServer(cli client.Client, log logr.Logger, cfg *ServerConfig) *Server {
	// Set lease duration metrics
	setLeaseDuration("resume", cfg.ResumeLeaseDuration.Seconds())
	setLeaseDuration("suspend", cfg.SuspendLeaseDuration.Seconds())

	// Create lease manager for K8s Lease-based resume gating
	// Use configured values or defaults
	leaseNamespace := cfg.LeaseNamespace
	if leaseNamespace == "" {
		leaseNamespace = config.APIServerDefaultLeaseNamespace
	}

	holderIdentity := cfg.LeaseHolderIdentity
	if holderIdentity == "" {
		holderIdentity = "xtrinode-api-server" // Default identity
	}

	leaseManager := NewLeaseManager(cli, log, leaseNamespace, cfg.ResumeLeaseDuration, holderIdentity)
	leaseManager.SetSuspendDuration(cfg.SuspendLeaseDuration)

	s := &Server{
		client:       cli,
		log:          log,
		config:       *cfg,
		leaseManager: leaseManager,
	}

	mux := http.NewServeMux()
	runtimesPath := cfg.APIPath + "/runtimes"
	mux.HandleFunc(runtimesPath+"/", s.apiHandler(s.handleRuntime))
	mux.HandleFunc(runtimesPath, s.apiHandler(s.handleRuntimes))

	// Unified resume endpoint (K8s Lease-based gating)
	resumePath := cfg.APIPath + "/resume"
	mux.HandleFunc(resumePath, s.apiHandler(s.handleUnifiedResume))

	// Standard Prometheus metrics endpoint: /metrics (for API server metrics)
	mux.Handle(cfg.MetricsPath, promhttp.Handler())

	mux.HandleFunc(cfg.HealthPath, s.handleHealth)

	s.server = &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadTimeout,  // Protects against Slowloris attacks (gosec G112)
		WriteTimeout:      cfg.WriteTimeout, // Aggregation might take time with many pods
	}

	return s
}

func (s *Server) apiHandler(next http.HandlerFunc) http.HandlerFunc {
	authConfig := bearerAuthConfig{
		Enabled:         s.config.AuthEnabled,
		AdminToken:      s.config.AuthToken,
		ResumeOnlyToken: s.config.ResumeAuthToken,
	}
	return withCORS(s.config.CORSAllowedOrigins, withRequestID(withBearerAuth(authConfig, next)))
}

// Start starts the HTTP server (implements manager.Runnable)
func (s *Server) Start(ctx context.Context) error {
	s.log.Info("Starting REST API server", "port", s.server.Addr)

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		s.log.Info("Shutting down REST API server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.config.ShutdownTimeout)
		defer cancel()
		return s.server.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

// NeedLeaderElection returns false - REST API server doesn't need leader election
func (s *Server) NeedLeaderElection() bool {
	return false
}

var _ manager.Runnable = &Server{}

// Helper methods for consistent error handling and responses

// writeJSON writes a JSON response
func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.log.Error(err, "Failed to encode JSON response")
	}
}

// writeError writes a standardized error response
func (s *Server) writeError(w http.ResponseWriter, status int, message, code string) {
	err := ErrorResponse{
		Error: message,
		Code:  code,
	}
	s.writeJSON(w, status, err)
}

func normalizeXTrinodeTypeMeta(xtrinode *analyticsv1.XTrinode) {
	if xtrinode.APIVersion == "" {
		xtrinode.APIVersion = analyticsv1.GroupVersion.String()
	}
	if xtrinode.Kind == "" {
		xtrinode.Kind = "XTrinode"
	}
}

// parsePath safely parses URL path into components
func parsePath(urlPath string) []string {
	// Clean the path and trim slashes
	cleaned := path.Clean(urlPath)
	trimmed := strings.Trim(cleaned, "/")

	if trimmed == "" || trimmed == "." {
		return []string{}
	}

	// Split and URL-decode each component
	parts := strings.Split(trimmed, "/")
	result := make([]string, 0, len(parts))

	for _, part := range parts {
		if part == "" {
			continue
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			// If decode fails, use original (safer than failing)
			result = append(result, part)
		} else {
			result = append(result, decoded)
		}
	}

	return result
}

// handleHealth handles health check requests
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	//nolint:errcheck // best-effort response write; failure is non-fatal
	_, _ = w.Write([]byte("OK"))
}

// handleRuntimes handles runtime list and creation
func (s *Server) handleRuntimes(w http.ResponseWriter, r *http.Request) {
	// Add per-request timeout (consistent with handleRuntime)
	ctx, cancel := context.WithTimeout(r.Context(), s.config.RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	switch r.Method {
	case http.MethodGet:
		if !s.authorize(w, r, apiActionRuntimeRead) {
			return
		}
		s.listRuntimes(w, r)
	case http.MethodPost:
		if !s.authorize(w, r, apiActionRuntimeCreate) {
			return
		}
		s.createRuntime(w, r)
	default:
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", "METHOD_NOT_ALLOWED")
	}
}

// handleRuntime handles individual runtime operations
func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	// Add per-request timeout
	ctx, cancel := context.WithTimeout(r.Context(), s.config.RequestTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	// Parse path: {apiPath}/runtimes/{namespace}/{name}/...
	runtimesPrefix := s.config.APIPath + "/runtimes/"
	pathSuffix := r.URL.Path[len(runtimesPrefix):]
	parts := parsePath(pathSuffix)

	if len(parts) < 2 {
		s.writeError(w, http.StatusBadRequest,
			fmt.Sprintf("Invalid path. Expected %s/runtimes/{namespace}/{name}", s.config.APIPath),
			"INVALID_PATH")
		return
	}

	namespace := parts[0]
	name := parts[1]
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}

	// Validate namespace and name
	if err := validateNamespace(namespace); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid namespace: %v", err), "INVALID_NAMESPACE")
		return
	}
	if err := validateK8sName(name); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid name: %v", err), "INVALID_NAME")
		return
	}

	// Route based on action and enforce HTTP method semantics
	switch action {
	case "resume":
		// POST only for mutations
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "Resume requires POST method", "METHOD_NOT_ALLOWED")
			return
		}
		if !s.authorize(w, r, apiActionRuntimeResume) {
			return
		}
		s.resumeRuntime(w, r, namespace, name)
	case "suspend":
		// POST only for mutations
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "Suspend requires POST method", "METHOD_NOT_ALLOWED")
			return
		}
		if !s.authorize(w, r, apiActionRuntimeSuspend) {
			return
		}
		s.suspendRuntime(w, r, namespace, name)
	case "status":
		// GET only for reads
		if r.Method != http.MethodGet {
			s.writeError(w, http.StatusMethodNotAllowed, "Status requires GET method", "METHOD_NOT_ALLOWED")
			return
		}
		if !s.authorize(w, r, apiActionRuntimeRead) {
			return
		}
		s.getRuntimeStatus(w, r, namespace, name)
	case "":
		switch r.Method {
		case http.MethodGet:
			if !s.authorize(w, r, apiActionRuntimeRead) {
				return
			}
			s.getRuntime(w, r, namespace, name)
		case http.MethodDelete:
			if !s.authorize(w, r, apiActionRuntimeDelete) {
				return
			}
			s.deleteRuntime(w, r, namespace, name)
		default:
			s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed", "METHOD_NOT_ALLOWED")
		}
	default:
		s.writeError(w, http.StatusBadRequest, "Unknown action", "UNKNOWN_ACTION")
	}
}

// listRuntimes lists all runtimes
func (s *Server) listRuntimes(w http.ResponseWriter, r *http.Request) {
	var xtrinodeList analyticsv1.XTrinodeList
	if err := s.client.List(r.Context(), &xtrinodeList); err != nil {
		s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to list runtimes: %v", err), "LIST_FAILED")
		return
	}

	for i := range xtrinodeList.Items {
		normalizeXTrinodeTypeMeta(&xtrinodeList.Items[i])
	}

	s.writeJSON(w, http.StatusOK, xtrinodeList.Items)
}

// getRuntime gets a specific runtime
func (s *Server) getRuntime(w http.ResponseWriter, r *http.Request, namespace, name string) {
	xtrinode := &analyticsv1.XTrinode{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, xtrinode); err != nil {
		if client.IgnoreNotFound(err) == nil {
			s.writeError(w, http.StatusNotFound, "Runtime not found", "NOT_FOUND")
		} else {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get runtime: %v", err), "GET_FAILED")
		}
		return
	}

	normalizeXTrinodeTypeMeta(xtrinode)
	s.writeJSON(w, http.StatusOK, xtrinode)
}

// createRuntime creates a new runtime
func (s *Server) createRuntime(w http.ResponseWriter, r *http.Request) {
	// Parse request body into safe DTO
	var req CreateRuntimeRequest
	// Limit request body size to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "INVALID_REQUEST")
		return
	}

	// Validate required fields
	if req.Name == "" {
		s.writeError(w, http.StatusBadRequest, "Name is required", "MISSING_NAME")
		return
	}
	if req.Namespace == "" {
		s.writeError(w, http.StatusBadRequest, "Namespace is required", "MISSING_NAMESPACE")
		return
	}
	if req.Size == "" {
		s.writeError(w, http.StatusBadRequest, "Size is required", "MISSING_SIZE")
		return
	}

	// Validate fields
	if err := validateK8sName(req.Name); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid name: %v", err), "INVALID_NAME")
		return
	}
	if err := validateNamespace(req.Namespace); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid namespace: %v", err), "INVALID_NAMESPACE")
		return
	}
	if err := validateSize(req.Size); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid size: %v", err), "INVALID_SIZE")
		return
	}
	if len(req.Labels) > 0 {
		if err := validateLabels(req.Labels); err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid labels: %v", err), "INVALID_LABELS")
			return
		}
	}

	// Construct CR from safe DTO
	xtrinode := &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    req.Labels,
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: req.Size,
		},
	}

	// Add routing if provided
	if req.Routing != nil {
		xtrinode.Spec.Routing = &analyticsv1.RoutingSpec{
			RoutingGroup: req.Routing.RoutingGroup,
		}
	}

	if err := s.client.Create(r.Context(), xtrinode); err != nil {
		if client.IgnoreAlreadyExists(err) == nil {
			s.writeError(w, http.StatusConflict, fmt.Sprintf("Runtime %s/%s already exists", req.Namespace, req.Name), "ALREADY_EXISTS")
			return
		}
		s.log.Error(err, "Failed to create runtime", "namespace", req.Namespace, "name", req.Name)
		s.writeError(w, http.StatusInternalServerError, "Failed to create runtime", "CREATE_FAILED")
		return
	}

	s.writeJSON(w, http.StatusCreated, xtrinode)
}

// deleteRuntime deletes a runtime
func (s *Server) deleteRuntime(w http.ResponseWriter, r *http.Request, namespace, name string) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}

	if err := s.client.Delete(r.Context(), xtrinode); err != nil {
		if client.IgnoreNotFound(err) == nil {
			s.writeError(w, http.StatusNotFound, "Runtime not found", "NOT_FOUND")
		} else {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to delete runtime: %v", err), "DELETE_FAILED")
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// resumeRuntime resumes a suspended runtime
func (s *Server) resumeRuntime(w http.ResponseWriter, r *http.Request, namespace, name string) {
	start := time.Now()
	var result string
	defer func() {
		observeRequestDuration("resume", result, time.Since(start).Seconds())
	}()

	// Parse optional body for wakeMinWorkers, wakeTTL
	var req ResumeRuntimeRequest
	if r.Body != nil && r.ContentLength > 0 {
		// Limit request body size to 1MB
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			result = "error"
			recordResumeRequest(result)
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "INVALID_REQUEST")
			return
		}
	}

	// Try acquire K8s Lease for this runtime
	key := MakeRuntimeKey(namespace, name)
	keyType := LeaseKeyTypeRuntime

	leaseResult, err := s.leaseManager.AcquireLease(r.Context(), key, keyType)
	if err != nil {
		result = "error"
		recordResumeRequest(result)
		recordK8sLeaseError(string(keyType))
		s.log.Error(err, "Failed to acquire lease", "namespace", namespace, "name", name, "key", key)
		s.writeError(w, http.StatusInternalServerError, "Failed to acquire lease", "LEASE_ERROR")
		return
	}

	if !leaseResult.Acquired {
		// Gated - return 503 Service Unavailable (same as /resume)
		recordK8sLeaseGated(string(keyType))
		recordResumeRequest("gated")
		result = "gated"

		remaining := int(time.Until(leaseResult.LeaseUntil).Seconds())
		if remaining < 1 {
			remaining = 30
		}
		if remaining > 120 {
			remaining = 120
		}

		response := map[string]interface{}{
			"status":     "gated",
			"operation":  "resume",
			"namespace":  namespace,
			"name":       name,
			"retryAfter": remaining,
			"leaseUntil": leaseResult.LeaseUntil.Format(time.RFC3339),
			"holder":     leaseResult.Holder,
			"message":    "Resume operation in progress by another request",
		}

		w.Header().Set("Retry-After", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-Lease-Gated", "true")
		w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
		w.Header().Set("X-Lease-Holder", leaseResult.Holder)
		s.writeJSON(w, http.StatusServiceUnavailable, response)
		return
	}

	// Apply resume annotations with RetryOnConflict (operator may update concurrently)
	updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		xtrinode := &analyticsv1.XTrinode{}
		if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, xtrinode); err != nil {
			return err
		}

		if xtrinode.Annotations == nil {
			xtrinode.Annotations = make(map[string]string)
		}

		now := time.Now().UTC()
		xtrinode.Annotations[config.ResumeRequestedAnnotation] = "true"
		xtrinode.Annotations[config.ResumeRequestedAtAnnotation] = now.Format(time.RFC3339)

		if req.WakeMinWorkers != nil {
			xtrinode.Annotations[config.WakeMinWorkersAnnotation] = fmt.Sprintf("%d", *req.WakeMinWorkers)
		}
		if req.WakeTTL != nil {
			xtrinode.Annotations[config.WakeTTLAnnotation] = req.WakeTTL.Duration.String()
		}

		return s.client.Update(r.Context(), xtrinode)
	})

	if updateErr != nil {
		result = "error"
		recordResumeRequest(result)
		// Release lease on failure to prevent unnecessary gating
		if releaseErr := s.leaseManager.ReleaseLease(r.Context(), key, keyType); releaseErr != nil {
			s.log.Error(releaseErr, "Failed to release lease after resume failure", "key", key)
		}
		if client.IgnoreNotFound(updateErr) == nil {
			s.writeError(w, http.StatusNotFound, "Runtime not found", "NOT_FOUND")
		} else {
			s.log.Error(updateErr, "Failed to update runtime with resume annotations", "namespace", namespace, "name", name)
			s.writeError(w, http.StatusInternalServerError, "Failed to trigger resume", "UPDATE_FAILED")
		}
		return
	}

	recordK8sUpdate("resume")
	recordResumeRequest("triggered")
	result = "triggered"

	s.log.Info("Resume triggered",
		"namespace", namespace,
		"name", name,
		"leaseUntil", leaseResult.LeaseUntil.Format(time.RFC3339))

	// Return 202 Accepted (lease acquired, resume triggered)
	retryAfter := s.config.RetryAfterSeconds
	if retryAfter <= 0 {
		retryAfter = 30
	}

	response := AsyncOperationResponse{
		Status:       "accepted",
		Desired:      "resumed",
		CurrentPhase: "", // Phase may have changed during retry; poll /status for current
		PollURL:      fmt.Sprintf("%s/runtimes/%s/%s/status", s.config.APIPath, namespace, name),
		Lease: LeaseInfo{
			Operation: "resume",
			Applied:   true,
			Until:     leaseResult.LeaseUntil.Format(time.RFC3339),
		},
		RetryAfterSeconds: retryAfter,
	}

	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	w.Header().Set("X-Lease-Acquired", "true")
	w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
	s.writeJSON(w, http.StatusAccepted, response)
}

// suspendRuntime suspends a runtime
func (s *Server) suspendRuntime(w http.ResponseWriter, r *http.Request, namespace, name string) {
	start := time.Now()
	var result string
	defer func() {
		observeRequestDuration("suspend", result, time.Since(start).Seconds())
	}()

	// Parse optional body (currently no parameters needed for suspend, but keep for future)
	var req SuspendRuntimeRequest
	if r.Body != nil && r.ContentLength > 0 {
		// Limit request body size to 1MB
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			result = "error"
			recordSuspendRequest(result)
			s.writeError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "INVALID_REQUEST")
			return
		}
	}

	// Try acquire K8s Lease for this runtime (suspend uses dedicated key type for separate duration)
	key := MakeRuntimeKey(namespace, name)
	keyType := LeaseKeyTypeSuspend

	leaseResult, err := s.leaseManager.AcquireLease(r.Context(), key, keyType)
	if err != nil {
		result = "error"
		recordSuspendRequest(result)
		recordK8sLeaseError(string(keyType))
		s.log.Error(err, "Failed to acquire lease", "namespace", namespace, "name", name, "key", key)
		s.writeError(w, http.StatusInternalServerError, "Failed to acquire lease", "LEASE_ERROR")
		return
	}

	if !leaseResult.Acquired {
		// Gated - return 503 Service Unavailable (same as /resume)
		recordK8sLeaseGated(string(keyType))
		recordSuspendRequest("gated")
		result = "gated"

		remaining := int(time.Until(leaseResult.LeaseUntil).Seconds())
		if remaining < 1 {
			remaining = 30
		}
		if remaining > 120 {
			remaining = 120
		}

		response := map[string]interface{}{
			"status":     "gated",
			"operation":  "suspend",
			"namespace":  namespace,
			"name":       name,
			"retryAfter": remaining,
			"leaseUntil": leaseResult.LeaseUntil.Format(time.RFC3339),
			"holder":     leaseResult.Holder,
			"message":    "Suspend operation in progress by another request",
		}

		w.Header().Set("Retry-After", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-Lease-Gated", "true")
		w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
		w.Header().Set("X-Lease-Holder", leaseResult.Holder)
		s.writeJSON(w, http.StatusServiceUnavailable, response)
		return
	}

	// Apply suspend annotations with RetryOnConflict (operator may update concurrently)
	updateErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		xtrinode := &analyticsv1.XTrinode{}
		if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, xtrinode); err != nil {
			return err
		}

		if xtrinode.Annotations == nil {
			xtrinode.Annotations = make(map[string]string)
		}

		now := time.Now().UTC()
		xtrinode.Annotations[config.SuspendRequestedAnnotation] = "true"
		xtrinode.Annotations[config.SuspendRequestedAtAnnotation] = now.Format(time.RFC3339)

		return s.client.Update(r.Context(), xtrinode)
	})

	if updateErr != nil {
		result = "error"
		recordSuspendRequest(result)
		// Release lease on failure to prevent unnecessary gating
		if releaseErr := s.leaseManager.ReleaseLease(r.Context(), key, keyType); releaseErr != nil {
			s.log.Error(releaseErr, "Failed to release lease after suspend failure", "key", key)
		}
		if client.IgnoreNotFound(updateErr) == nil {
			s.writeError(w, http.StatusNotFound, "Runtime not found", "NOT_FOUND")
		} else {
			s.log.Error(updateErr, "Failed to update runtime with suspend annotations", "namespace", namespace, "name", name)
			s.writeError(w, http.StatusInternalServerError, "Failed to trigger suspend", "UPDATE_FAILED")
		}
		return
	}

	recordK8sUpdate("suspend")
	recordSuspendRequest("triggered")
	result = "triggered"

	s.log.Info("Suspend triggered",
		"namespace", namespace,
		"name", name,
		"leaseUntil", leaseResult.LeaseUntil.Format(time.RFC3339))

	// Return 202 Accepted (lease acquired, suspend triggered)
	retryAfter := s.config.RetryAfterSeconds
	if retryAfter <= 0 {
		retryAfter = 30
	}

	response := AsyncOperationResponse{
		Status:       "accepted",
		Desired:      "suspended",
		CurrentPhase: "", // Phase may have changed during retry; poll /status for current
		PollURL:      fmt.Sprintf("%s/runtimes/%s/%s/status", s.config.APIPath, namespace, name),
		Lease: LeaseInfo{
			Operation: "suspend",
			Applied:   true,
			Until:     leaseResult.LeaseUntil.Format(time.RFC3339),
		},
		RetryAfterSeconds: retryAfter,
	}

	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	w.Header().Set("X-Lease-Acquired", "true")
	w.Header().Set("X-Lease-Until", leaseResult.LeaseUntil.Format(time.RFC3339))
	s.writeJSON(w, http.StatusAccepted, response)
}

// getRuntimeStatus gets runtime status
func (s *Server) getRuntimeStatus(w http.ResponseWriter, r *http.Request, namespace, name string) {
	xtrinode := &analyticsv1.XTrinode{}
	if err := s.client.Get(r.Context(), types.NamespacedName{Namespace: namespace, Name: name}, xtrinode); err != nil {
		if client.IgnoreNotFound(err) == nil {
			s.writeError(w, http.StatusNotFound, "Runtime not found", "NOT_FOUND")
		} else {
			s.writeError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to get runtime: %v", err), "GET_FAILED")
		}
		return
	}

	status := map[string]interface{}{
		"phase":          xtrinode.Status.Phase,
		"coordinatorURL": xtrinode.Status.CoordinatorURL,
		"workers":        xtrinode.Status.Workers,
		"lastActivity":   xtrinode.Status.LastActivity,
		"conditions":     xtrinode.Status.Conditions,
	}

	s.writeJSON(w, http.StatusOK, status)
}
