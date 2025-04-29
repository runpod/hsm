package elements

import "github.com/runpod/hsm/v2/muid"

type Type interface{}

type Element interface {
	Kind() uint64
	Id() string
}

type NamedElement interface {
	Element
	Owner() string
	QualifiedName() string
	Name() string
}

type Namespace interface {
	NamedElement
	Members() map[string]NamedElement
}

type Model interface {
	Namespace
}

type Transition interface {
	NamedElement
	Source() string
	Target() string
	Guard() string
	Effect() []string
	Events() []Event
}

type Vertex interface {
	NamedElement
	Transitions() []string
}

type State interface {
	Vertex
	Entry() []string
	Activities() []string
	Exit() []string
}

type Event struct {
	Kind uint64    `json:"kind"`
	Name string    `json:"name"`
	Id   muid.MUID `json:"id"`
	Data any       `json:"data"`

	// Deprecated: HSM now waits for all events by default
	Done chan struct{} `json:"-"`
}

func (e Event) WithData(data any, maybeDone ...chan struct{}) Event {
	var done chan struct{}
	if len(maybeDone) > 0 {
		done = maybeDone[0]
	}
	return Event{
		Kind: e.Kind,
		Name: e.Name,
		Id:   e.Id,
		Data: data,
		Done: done,
	}
}

// Deprecated: Events can't wait anymore, hsm processing waits for all events by default
func (e Event) WithDone(done chan struct{}) Event {
	return Event{
		Kind: e.Kind,
		Name: e.Name,
		Id:   e.Id,
		Data: e.Data,
		Done: done,
	}
}

type Constraint interface {
	NamedElement
	Expression() any
}

type Behavior interface {
	NamedElement
	Action() any
}
