package gateway

import "time"

// BackendLoad tracks recently observed query activity for a backend.
type BackendLoad struct {
	RunningQueries int       `json:"runningQueries"`
	QueuedQueries  int       `json:"queuedQueries"`
	LastUpdate     time.Time `json:"lastUpdate"`
}
