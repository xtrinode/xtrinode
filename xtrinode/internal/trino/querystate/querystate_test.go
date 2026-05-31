package querystate

import "testing"

func TestIsTerminal(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "FINISHED", want: true},
		{name: "FAILED", want: true},
		{name: "CANCELED", want: true},
		{name: alternateCanceledState, want: true},
		{name: "RUNNING", want: false},
		{name: "QUEUED", want: false},
		{name: "PLANNING", want: false},
		{name: "WAITING_FOR_RESOURCES", want: false},
		{name: "STARTING", want: false},
		{name: "FINISHING", want: false},
		{name: "", want: false},
		{name: " unknown ", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTerminal(tt.name); got != tt.want {
				t.Fatalf("IsTerminal(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	if got := Normalize(" running "); got != "RUNNING" {
		t.Fatalf("Normalize returned %q, want RUNNING", got)
	}
	if got := Normalize(""); got != "UNKNOWN" {
		t.Fatalf("Normalize returned %q, want UNKNOWN", got)
	}
}
