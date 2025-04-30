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
	Events() []string
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
}

func (e Event) WithData(data any) Event {
	return Event{
		Kind: e.Kind,
		Name: e.Name,
		Id:   e.Id,
		Data: data,
	}
}

// Deprecated: Events can't wait anymore, hsm processing waits for all events by default
func (e Event) WithDone(done chan struct{}) Event {
	return Event{
		Kind: e.Kind,
		Name: e.Name,
		Id:   e.Id,
		Data: e.Data,
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
