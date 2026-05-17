package status

import "fmt"

// Phase represents the lifecycle phase of a XTrinode.
type Phase string

const (
	// PhasePending indicates the XTrinode is newly created and awaiting initial reconciliation.
	PhasePending Phase = "Pending"

	// PhaseReconciling indicates the XTrinode is being actively reconciled.
	PhaseReconciling Phase = "Reconciling"

	// PhaseReady indicates all components are ready and operational.
	PhaseReady Phase = "Ready"

	// PhaseSuspending indicates the XTrinode is transitioning to suspended state.
	PhaseSuspending Phase = "Suspending"

	// PhaseSuspended indicates the XTrinode is suspended with replicas at zero.
	PhaseSuspended Phase = "Suspended"

	// PhaseResuming indicates the XTrinode is transitioning from suspended to ready.
	PhaseResuming Phase = "Resuming"

	// PhaseError indicates an error occurred during reconciliation.
	PhaseError Phase = "Error"
)

// ValidPhases returns all valid phase values.
func ValidPhases() []Phase {
	return []Phase{
		PhasePending,
		PhaseReconciling,
		PhaseReady,
		PhaseSuspending,
		PhaseSuspended,
		PhaseResuming,
		PhaseError,
	}
}

// IsValid checks if a phase is valid.
func (p Phase) IsValid() bool {
	for _, valid := range ValidPhases() {
		if p == valid {
			return true
		}
	}
	return false
}

// TransitionTo validates and returns an error if the transition is invalid.
func (p Phase) TransitionTo(next Phase) error {
	if !next.IsValid() {
		return fmt.Errorf("invalid target phase: %s", next)
	}

	validTransitions := map[Phase][]Phase{
		PhasePending: {
			PhaseReconciling,
			PhaseError,
		},
		PhaseReconciling: {
			PhaseReady,
			PhaseSuspending,
			PhaseError,
		},
		PhaseReady: {
			PhaseSuspending,
			PhaseReconciling,
			PhaseError,
		},
		PhaseSuspending: {
			PhaseSuspended,
			PhaseError,
		},
		PhaseSuspended: {
			PhaseResuming,
			PhaseError,
		},
		PhaseResuming: {
			PhaseReconciling,
			PhaseError,
		},
		PhaseError: {
			PhaseReconciling,
			PhaseSuspending,
			PhaseResuming,
		},
	}

	for _, valid := range validTransitions[p] {
		if next == valid {
			return nil
		}
	}

	return fmt.Errorf("invalid phase transition: %s -> %s", p, next)
}

// CanTransitionTo checks if a transition is valid without returning an error.
func (p Phase) CanTransitionTo(next Phase) bool {
	return p.TransitionTo(next) == nil
}

// String returns the string representation of the phase.
func (p Phase) String() string {
	return string(p)
}

// IsTerminal returns true if this is a terminal phase.
func (p Phase) IsTerminal() bool {
	return p == PhaseError
}

// RequiresAction returns true if this phase requires controller action.
func (p Phase) RequiresAction() bool {
	return p == PhasePending || p == PhaseReconciling || p == PhaseSuspending || p == PhaseResuming
}
