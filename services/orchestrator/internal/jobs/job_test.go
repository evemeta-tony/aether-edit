// services/orchestrator/internal/jobs/job_test.go
package jobs

import (
	"errors"
	"testing"
)

func TestStateMachineTransitions(t *testing.T) {
	all := []State{StateQueued, StateRunning, StateCompleted, StateFailed}
	allowed := map[[2]State]bool{
		{StateQueued, StateRunning}:    true,
		{StateQueued, StateFailed}:     true, // cancel while queued
		{StateRunning, StateCompleted}: true,
		{StateRunning, StateFailed}:    true, // failure or cancel while running
		{StateFailed, StateQueued}:     true, // retry
	}
	for _, from := range all {
		for _, to := range all {
			got := CanTransition(from, to)
			want := allowed[[2]State{from, to}]
			if got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
			err := Transition(from, to)
			if want && err != nil {
				t.Errorf("Transition(%s, %s) unexpected error: %v", from, to, err)
			}
			if !want && !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("Transition(%s, %s) = %v, want ErrInvalidTransition", from, to, err)
			}
		}
	}
}

func TestTransitionRejectsUnknownStates(t *testing.T) {
	if err := Transition(State("paused"), StateRunning); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("unknown from-state: got %v", err)
	}
	if err := Transition(StateQueued, State("archived")); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("unknown to-state: got %v", err)
	}
}

func TestRetryOnlyFromFailed(t *testing.T) {
	if !CanRetry(StateFailed) {
		t.Error("failed must be retryable")
	}
	for _, s := range []State{StateQueued, StateRunning, StateCompleted} {
		if CanRetry(s) {
			t.Errorf("state %s must not be retryable", s)
		}
	}
}

func TestCancelOnlyFromQueuedOrRunning(t *testing.T) {
	for _, s := range []State{StateQueued, StateRunning} {
		if !CanCancel(s) {
			t.Errorf("state %s must be cancelable", s)
		}
	}
	for _, s := range []State{StateCompleted, StateFailed} {
		if CanCancel(s) {
			t.Errorf("state %s must not be cancelable", s)
		}
	}
}

func TestErrorTaxonomy(t *testing.T) {
	for _, c := range []ErrorClass{ErrorValidation, ErrorAsset, ErrorDecode, ErrorEncode, ErrorInternal} {
		if !ValidErrorClass(c) {
			t.Errorf("class %s must be valid", c)
		}
	}
	if ValidErrorClass(ErrorClass("oom")) {
		t.Error("unknown class must be invalid")
	}
}
