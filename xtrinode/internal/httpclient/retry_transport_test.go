package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestRetryTransport_RetriesOn429(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	rt := NewRetryTransportWithConfig(RetryConfig{
		MaxRetries: 2,
		BaseDelay:  0,
		MaxDelay:   0,
		Timeout:    2 * time.Second,
	}, logr.Discard())

	client := &http.Client{Transport: rt}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body failed: %v", err)
	}
	if got := string(body); got != "ok" {
		t.Fatalf("unexpected body: %q", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}

func TestRetryTransport_DoesNotCancelBeforeBodyRead(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", 64*1024)))
	}))
	t.Cleanup(srv.Close)

	rt := NewRetryTransportWithConfig(RetryConfig{
		MaxRetries: 0,
		BaseDelay:  0,
		MaxDelay:   0,
		Timeout:    2 * time.Second,
	}, logr.Discard())

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}

	_, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()

	if readErr != nil {
		t.Fatalf("body read failed: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("body close failed: %v", closeErr)
	}
}

type sequencingRoundTripper struct {
	calls            atomic.Int64
	firstBodyClosed  chan struct{}
	secondCallChecks func() error
	onFirstCall      func()
}

func (s *sequencingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	n := s.calls.Add(1)
	switch n {
	case 1:
		if s.onFirstCall != nil {
			s.onFirstCall()
		}
		body := &trackingBody{
			ReadCloser:       io.NopCloser(strings.NewReader("temporary error")),
			closedSignalOnce: make(chan struct{}),
		}
		s.firstBodyClosed = body.closedSignalOnce
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       body,
			Header:     make(http.Header),
		}, nil
	case 2:
		if s.secondCallChecks != nil {
			if err := s.secondCallChecks(); err != nil {
				return nil, err
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Header:     make(http.Header),
		}, nil
	default:
		return nil, errors.New("unexpected call count")
	}
}

type trackingBody struct {
	io.ReadCloser
	closedSignalOnce chan struct{}
}

func (b *trackingBody) Close() error {
	err := b.ReadCloser.Close()
	select {
	case <-b.closedSignalOnce:
	default:
		close(b.closedSignalOnce)
	}
	return err
}

func TestRetryTransport_ClosesBodiesBetweenRetries(t *testing.T) {
	t.Parallel()

	seq := &sequencingRoundTripper{}
	seq.secondCallChecks = func() error {
		select {
		case <-seq.firstBodyClosed:
			return nil
		default:
			return errors.New("first response body not closed before retry")
		}
	}

	rt := &RetryTransport{
		Transport: seq,
		Config: RetryConfig{
			MaxRetries: 1,
			BaseDelay:  0,
			MaxDelay:   0,
			Timeout:    2 * time.Second,
		},
		Log: logr.Discard(),
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://example.test", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body failed: %v", err)
	}
	if got := string(body); got != "ok" {
		t.Fatalf("unexpected body: %q", got)
	}
}

func TestRetryTransport_BackoffStopsWhenContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	seq := &sequencingRoundTripper{
		onFirstCall: cancel,
	}

	rt := &RetryTransport{
		Transport: seq,
		Config: RetryConfig{
			MaxRetries: 1,
			BaseDelay:  time.Second,
			MaxDelay:   time.Second,
			Timeout:    2 * time.Second,
		},
		Log: logr.Discard(),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	start := time.Now()
	resp, err := rt.RoundTrip(req)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got response=%v error=%v", resp, err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("retry backoff did not stop promptly after cancellation: %v", elapsed)
	}
	if got := seq.calls.Load(); got != 1 {
		t.Fatalf("expected no retry after cancellation, got %d calls", got)
	}
}
