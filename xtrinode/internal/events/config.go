package events

import "time"

// Config defines configuration for event recording
type Config struct {
	// ComponentName is the component name used in event source
	ComponentName string

	// Enabled controls whether events are recorded
	// When false, events are silently ignored (useful for testing)
	Enabled bool

	// DeduplicationWindow is the time window for event deduplication
	// Kubernetes automatically deduplicates events within this window
	DeduplicationWindow time.Duration
}

// DefaultConfig returns the default event recording configuration
func DefaultConfig() Config {
	return Config{
		ComponentName:       "xtrinode-operator",
		Enabled:             true,
		DeduplicationWindow: 5 * time.Minute,
	}
}

// WithComponentName returns a new config with the specified component name
func (c Config) WithComponentName(name string) Config {
	c.ComponentName = name
	return c
}

// WithEnabled returns a new config with the specified enabled state
func (c Config) WithEnabled(enabled bool) Config {
	c.Enabled = enabled
	return c
}
