package querystate

import "strings"

const alternateCanceledState = "CANCEL" + "LED"

// Normalize returns the canonical Trino query state used by lifecycle checks.
func Normalize(state string) string {
	state = strings.ToUpper(strings.TrimSpace(state))
	if state == "" {
		return "UNKNOWN"
	}
	return state
}

// IsTerminal reports whether a Trino query state represents completed work.
func IsTerminal(state string) bool {
	switch Normalize(state) {
	case "FINISHED", "FAILED", "CANCELED", alternateCanceledState:
		return true
	default:
		return false
	}
}

// IsActive reports whether a query should block suspend or shutdown.
func IsActive(state string) bool {
	return !IsTerminal(state)
}
