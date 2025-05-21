package kind

import (
	"sync"
)

const (
	length   = 64
	idLength = 8
	depthMax = length / idLength
	idMask   = (1 << idLength) - 1
)

// TypeBases returns the "base" IDs at each level
// (beyond the first) by shifting and masking.
func Bases(t uint64) [depthMax]uint64 {
	var bases [depthMax]uint64
	for i := 1; i < depthMax; i++ {
		bases[i-1] = (t >> (idLength * i)) & idMask
	}
	return bases
}

func Kind(id uint64, bases ...uint64) uint64 {
	id = id & idMask
	ids := make(map[uint64]struct{})

	for _, base := range bases {
		for j := 0; j < depthMax; j++ {
			baseId := (base >> (idLength * j)) & idMask
			if baseId == 0 {
				break
			}
			if _, ok := ids[baseId]; !ok {
				ids[baseId] = struct{}{}
				id |= baseId << (idLength * len(ids))
			}
		}
	}
	return id
}

// IsBase checks if 'typeVal' matches any or all bases provided.
//
//go:inline
func IsKind(kind uint64, bases ...uint64) bool {
	for _, base := range bases {
		baseId := base & idMask
		if kind == baseId {
			return true
		}
		for i := 0; i < depthMax; i++ {
			currentId := (kind >> (idLength * i)) & idMask
			if currentId == baseId {
				return true
			}
		}
	}
	return false
}

type counter uint64

func Counter(maybeStart ...uint64) counter {
	if len(maybeStart) == 0 {
		return 0
	}
	return counter(maybeStart[0])
}

func (id *counter) Next() uint64 {
	next := *id
	*id++
	return uint64(next)
}

type kinds struct {
	Null            uint64
	Element         uint64
	Vertex          uint64
	Constraint      uint64
	Behavior        uint64
	Concurrent      uint64
	StateMachine    uint64
	Namespace       uint64
	Region          uint64
	State           uint64
	Transition      uint64
	Internal        uint64
	External        uint64
	Local           uint64
	Self            uint64
	Event           uint64
	CompletionEvent uint64
	ErrorEvent      uint64
	TimeEvent       uint64
	Pseudostate     uint64
	Initial         uint64
	FinalState      uint64
	Choice          uint64
	Custom          uint64
}

var (
	id              = Counter()
	Null            = Kind(id.Next())
	Element         = Kind(id.Next())
	Namespace       = Kind(id.Next(), Element)
	Vertex          = Kind(id.Next(), Element)
	Constraint      = Kind(id.Next(), Element)
	Behavior        = Kind(id.Next(), Element)
	Concurrent      = Kind(id.Next(), Behavior)
	StateMachine    = Kind(id.Next(), Behavior, Namespace)
	State           = Kind(id.Next(), Vertex, Namespace)
	Region          = Kind(id.Next(), Element)
	Transition      = Kind(id.Next(), Element)
	Internal        = Kind(id.Next(), Transition)
	External        = Kind(id.Next(), Transition)
	Local           = Kind(id.Next(), Transition)
	Self            = Kind(id.Next(), Transition)
	Event           = Kind(id.Next(), Element)
	CompletionEvent = Kind(id.Next(), Event)
	ErrorEvent      = Kind(id.Next(), CompletionEvent)
	TimeEvent       = Kind(id.Next(), Event)
	Pseudostate     = Kind(id.Next(), Vertex)
	Initial         = Kind(id.Next(), Pseudostate)
	FinalState      = Kind(id.Next(), State)
	Choice          = Kind(id.Next(), Pseudostate)
	Custom          = Kind(id.Next(), Element)
)

var Kinds = sync.OnceValue(func() (kinds kinds) {
	kinds.Null = Null
	kinds.Element = Element
	kinds.Vertex = Vertex
	kinds.Constraint = Constraint
	kinds.Behavior = Behavior
	kinds.Concurrent = Concurrent
	kinds.StateMachine = StateMachine
	kinds.Region = Region
	kinds.State = State
	kinds.Transition = Transition
	kinds.Namespace = Namespace
	kinds.Internal = Internal
	kinds.External = External
	kinds.Local = Local
	kinds.Self = Self
	kinds.Event = Event
	kinds.TimeEvent = TimeEvent
	kinds.CompletionEvent = CompletionEvent
	kinds.ErrorEvent = ErrorEvent
	kinds.Pseudostate = Pseudostate
	kinds.Initial = Initial
	kinds.FinalState = FinalState
	kinds.Choice = Choice
	kinds.Custom = Custom
	return kinds
})
