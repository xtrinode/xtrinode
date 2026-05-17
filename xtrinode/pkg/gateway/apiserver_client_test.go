package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIServerClientResume_ReturnsErrorForUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"No candidate runtime found"}`)
	}))
	defer server.Close()

	client := NewAPIServerClient(server.URL, 5*time.Second, logr.Discard())
	resp, err := client.Resume(context.Background(), ResumeRequest{RoutingGroup: "missing"})

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "status 404")
	assert.Contains(t, err.Error(), "No candidate runtime found")
}

func TestAPIServerClientResume_SendsBearerToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"triggered":true,"gated":false,"retryAfter":30}`)
	}))
	defer server.Close()

	client := NewAPIServerClientWithToken(server.URL, 5*time.Second, "test-token", logr.Discard())
	resp, err := client.Resume(context.Background(), ResumeRequest{RoutingGroup: "default"})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Triggered)
}

func TestAPIServerClientResume_RootBaseURLUsesDefaultAPIPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/resume", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"triggered":true}`)
	}))
	defer server.Close()

	client := NewAPIServerClient(server.URL, 5*time.Second, logr.Discard())
	resp, err := client.Resume(context.Background(), ResumeRequest{RoutingGroup: "default"})

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Triggered)
}
