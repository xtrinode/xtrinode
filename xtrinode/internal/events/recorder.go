package events

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// Recorder is an interface for recording Kubernetes events
// This abstraction allows for easy testing and potential future implementations
type Recorder interface {
	// Event records an event with the given type, reason, and message
	Event(object runtime.Object, eventType, reason, message string)

	// Eventf records an event with formatted message
	Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{})

	// Normal records a Normal event
	Normal(object runtime.Object, reason, message string)

	// Normalf records a Normal event with formatted message
	Normalf(object runtime.Object, reason, messageFmt string, args ...interface{})

	// Warning records a Warning event
	Warning(object runtime.Object, reason, message string)

	// Warningf records a Warning event with formatted message
	Warningf(object runtime.Object, reason, messageFmt string, args ...interface{})
}

// recorder implements the Recorder interface using Kubernetes EventRecorder
type recorder struct {
	recorder record.EventRecorder
	config   Config
}

// NewRecorder creates a new event recorder with the given Kubernetes EventRecorder and config
func NewRecorder(eventRecorder record.EventRecorder, config Config) Recorder {
	return &recorder{
		recorder: eventRecorder,
		config:   config,
	}
}

// Event records an event with the given type, reason, and message
func (r *recorder) Event(object runtime.Object, eventType, reason, message string) {
	if !r.config.Enabled {
		return
	}
	r.recorder.Event(object, eventType, reason, message)
}

// Eventf records an event with formatted message
func (r *recorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	if !r.config.Enabled {
		return
	}
	r.recorder.Eventf(object, eventType, reason, messageFmt, args...)
}

// Normal records a Normal event
func (r *recorder) Normal(object runtime.Object, reason, message string) {
	if !r.config.Enabled {
		return
	}
	r.recorder.Event(object, corev1.EventTypeNormal, reason, message)
}

// Normalf records a Normal event with formatted message
func (r *recorder) Normalf(object runtime.Object, reason, messageFmt string, args ...interface{}) {
	if !r.config.Enabled {
		return
	}
	r.recorder.Eventf(object, corev1.EventTypeNormal, reason, messageFmt, args...)
}

// Warning records a Warning event
func (r *recorder) Warning(object runtime.Object, reason, message string) {
	if !r.config.Enabled {
		return
	}
	r.recorder.Event(object, corev1.EventTypeWarning, reason, message)
}

// Warningf records a Warning event with formatted message
func (r *recorder) Warningf(object runtime.Object, reason, messageFmt string, args ...interface{}) {
	if !r.config.Enabled {
		return
	}
	r.recorder.Eventf(object, corev1.EventTypeWarning, reason, messageFmt, args...)
}

// FormatMessage formats a message with context for better observability
// This helper ensures consistent message formatting across the codebase
func FormatMessage(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}
