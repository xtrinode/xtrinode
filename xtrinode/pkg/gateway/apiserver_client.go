package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/xtrinode/xtrinode/internal/config"
)

// APIServerClient handles communication with the API server
type APIServerClient struct {
	baseURL     string
	httpClient  *http.Client
	bearerToken string
	log         logr.Logger
}

// NewAPIServerClient creates a new API server client
func NewAPIServerClient(baseURL string, timeout time.Duration, log logr.Logger) *APIServerClient {
	return NewAPIServerClientWithToken(baseURL, timeout, "", log)
}

// NewAPIServerClientWithToken creates a new API server client with optional bearer auth.
func NewAPIServerClientWithToken(baseURL string, timeout time.Duration, bearerToken string, log logr.Logger) *APIServerClient {
	return &APIServerClient{
		baseURL: normalizeAPIServerBaseURL(baseURL),
		httpClient: &http.Client{
			Timeout: timeout,
		},
		bearerToken: bearerToken,
		log:         log,
	}
}

func normalizeAPIServerBaseURL(rawURL string) string {
	baseURL := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return baseURL
	}
	if strings.Trim(parsed.Path, "/") == "" {
		parsed.Path = config.APIServerDefaultAPIPath
		parsed.RawPath = ""
	}
	return parsed.String()
}

// ResumeRequest is the request payload for the unified resume endpoint
type ResumeRequest struct {
	RoutingGroup string           `json:"routingGroup,omitempty"`
	Candidate    *ResumeCandidate `json:"candidate,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	RouteName    string           `json:"routeName,omitempty"`
}

// ResumeCandidate identifies a specific runtime to resume
type ResumeCandidate struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ResumeResponse is the response from the unified resume endpoint
type ResumeResponse struct {
	Triggered  bool   `json:"triggered"`
	Gated      bool   `json:"gated"`
	RetryAfter int    `json:"retryAfter"`
	Key        string `json:"key"`
	KeyType    string `json:"keyType"`
	LeaseUntil string `json:"leaseUntil"`
	Error      string `json:"error,omitempty"`
	Holder     string `json:"holder,omitempty"`
}

// Resume calls the unified resume endpoint
func (c *APIServerClient) Resume(ctx context.Context, req ResumeRequest) (*ResumeResponse, error) {
	urlStr, err := url.JoinPath(c.baseURL, "resume")
	if err != nil {
		return nil, fmt.Errorf("failed to build resume URL: %w", err)
	}

	// Marshal request body
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal resume request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.bearerToken)
	}

	// Make HTTP call
	start := time.Now()
	httpResp, err := c.httpClient.Do(httpReq)
	duration := time.Since(start).Seconds()

	if err != nil {
		recordResumeAPICall("error", duration)
		return nil, fmt.Errorf("failed to call API server: %w", err)
	}
	defer httpResp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		recordResumeAPICall("error", duration)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Record metrics based on status code
	switch httpResp.StatusCode {
	case http.StatusAccepted:
		recordResumeAPICall("202", duration)
	case http.StatusServiceUnavailable:
		recordResumeAPICall("503", duration)
	default:
		recordResumeAPICall(fmt.Sprintf("%d", httpResp.StatusCode), duration)
	}

	if httpResp.StatusCode != http.StatusAccepted && httpResp.StatusCode != http.StatusServiceUnavailable {
		message := strings.TrimSpace(string(respBody))
		if message == "" {
			message = http.StatusText(httpResp.StatusCode)
		}
		if len(message) > 512 {
			message = message[:512]
		}
		return nil, fmt.Errorf("api server resume failed with status %d: %s", httpResp.StatusCode, message)
	}

	// Parse response
	var resp ResumeResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		recordResumeAPICall("error", duration)
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	c.log.V(1).Info("Resume API call completed",
		"endpoint", urlStr,
		"status", httpResp.StatusCode,
		"triggered", resp.Triggered,
		"gated", resp.Gated,
		"retryAfter", resp.RetryAfter,
		"duration", duration)

	return &resp, nil
}

// recordResumeAPICall records metrics for resume API calls
func recordResumeAPICall(status string, duration float64) {
	if gatewayResumeAPICallsTotal != nil {
		gatewayResumeAPICallsTotal.WithLabelValues(status).Inc()
	}
	if gatewayResumeAPICallDuration != nil {
		gatewayResumeAPICallDuration.Observe(duration)
	}
}
