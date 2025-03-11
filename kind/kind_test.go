package kind

import (
	"testing"
)

func TestKinds(t *testing.T) {
	if !IsKind(StateMachine, Behavior) {
		t.Errorf("StateMachine should be a Behavior")
	}
	if IsKind(StateMachine, Vertex) {
		t.Errorf("StateMachine should not be a Vertex")
	}
	if !IsKind(State, Vertex) {
		t.Errorf("State should be a Vertex")
	}
	if IsKind(State, Behavior) {
		t.Errorf("State should not be a Behavior")
	}
	if !IsKind(Choice, Pseudostate) {
		t.Errorf("Choice should be a PseudoState")
	}
	if !IsKind(Choice, Vertex) {
		t.Errorf("Choice should not be a Vertex")
	}
	if !IsKind(ErrorEvent, CompletionEvent) {
		t.Errorf("ErrorEvent should be an Event")
	}
	if IsKind(ErrorEvent, Vertex) {
		t.Errorf("ErrorEvent should not be a Vertex")
	}

}
