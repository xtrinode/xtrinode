package status

import "testing"

func TestPhaseTransitions(t *testing.T) {
	tests := []struct {
		name      string
		from      Phase
		to        Phase
		wantError bool
	}{
		{"Pending to Reconciling", PhasePending, PhaseReconciling, false},
		{"Pending to Error", PhasePending, PhaseError, false},
		{"Reconciling to Ready", PhaseReconciling, PhaseReady, false},
		{"Reconciling to Suspending", PhaseReconciling, PhaseSuspending, false},
		{"Reconciling to Error", PhaseReconciling, PhaseError, false},
		{"Ready to Suspending", PhaseReady, PhaseSuspending, false},
		{"Ready to Reconciling", PhaseReady, PhaseReconciling, false},
		{"Ready to Error", PhaseReady, PhaseError, false},
		{"Suspending to Suspended", PhaseSuspending, PhaseSuspended, false},
		{"Suspending to Error", PhaseSuspending, PhaseError, false},
		{"Suspended to Resuming", PhaseSuspended, PhaseResuming, false},
		{"Suspended to Error", PhaseSuspended, PhaseError, false},
		{"Resuming to Reconciling", PhaseResuming, PhaseReconciling, false},
		{"Resuming to Error", PhaseResuming, PhaseError, false},
		{"Error to Reconciling", PhaseError, PhaseReconciling, false},
		{"Error to Suspending", PhaseError, PhaseSuspending, false},
		{"Error to Resuming", PhaseError, PhaseResuming, false},
		{"Suspended to Ready", PhaseSuspended, PhaseReady, true},
		{"Pending to Ready", PhasePending, PhaseReady, true},
		{"Pending to Suspended", PhasePending, PhaseSuspended, true},
		{"Ready to Suspended", PhaseReady, PhaseSuspended, true},
		{"Suspending to Ready", PhaseSuspending, PhaseReady, true},
		{"Resuming to Ready", PhaseResuming, PhaseReady, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.from.TransitionTo(tt.to)
			if (err != nil) != tt.wantError {
				t.Errorf("TransitionTo() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestPhaseIsValid(t *testing.T) {
	tests := []struct {
		name  string
		phase Phase
		want  bool
	}{
		{"Pending is valid", PhasePending, true},
		{"Reconciling is valid", PhaseReconciling, true},
		{"Ready is valid", PhaseReady, true},
		{"Suspending is valid", PhaseSuspending, true},
		{"Suspended is valid", PhaseSuspended, true},
		{"Resuming is valid", PhaseResuming, true},
		{"Error is valid", PhaseError, true},
		{"Invalid phase", Phase("Invalid"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.phase.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPhaseIsTerminal(t *testing.T) {
	tests := []struct {
		name  string
		phase Phase
		want  bool
	}{
		{"Error is terminal", PhaseError, true},
		{"Ready is not terminal", PhaseReady, false},
		{"Suspended is not terminal", PhaseSuspended, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.phase.IsTerminal(); got != tt.want {
				t.Errorf("IsTerminal() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPhaseRequiresAction(t *testing.T) {
	tests := []struct {
		name  string
		phase Phase
		want  bool
	}{
		{"Pending requires action", PhasePending, true},
		{"Reconciling requires action", PhaseReconciling, true},
		{"Suspending requires action", PhaseSuspending, true},
		{"Resuming requires action", PhaseResuming, true},
		{"Ready does not require action", PhaseReady, false},
		{"Suspended does not require action", PhaseSuspended, false},
		{"Error does not require action", PhaseError, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.phase.RequiresAction(); got != tt.want {
				t.Errorf("RequiresAction() = %v, want %v", got, tt.want)
			}
		})
	}
}
