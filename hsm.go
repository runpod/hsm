package hsm

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"log/slog"
	"path"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runpod/hsm/elements"
	"github.com/runpod/hsm/kind"
)

var (
	Kinds             = kind.Kinds()
	ErrNilHSM         = errors.New("hsm is nil")
	ErrInvalidState   = errors.New("invalid state")
	ErrMissingHSM     = errors.New("missing hsm in context")
	ErrInvalidPattern = errors.New("invalid pattern")
)

// Package hsm provides a powerful hierarchical state machine (HSM) implementation for Go.
// It enables modeling complex state-driven systems with features like hierarchical states,
// entry/exit actions, guard conditions, and event-driven transitions.
//
// Basic usage:
//
//	type MyHSM struct {
//	    hsm.HSM
//	    counter int
//	}
//
//	model := hsm.Define(
//	    "example",
//	    hsm.State("foo"),
//	    hsm.State("bar"),
//	    hsm.Transition(
//	        hsm.Trigger("moveToBar"),
//	        hsm.Source("foo"),
//	        hsm.Target("bar")
//	    ),
//	    hsm.Initial("foo")
//	)
//
//	sm := hsm.Start(context.Background(), &MyHSM{}, &model)
//	sm.Dispatch(hsm.Event{Name: "moveToBar"})

/******* Element *******/

type element struct {
	kind          uint64
	qualifiedName string
	id            string
}

func (element *element) Kind() uint64 {
	if element == nil {
		return 0
	}
	return element.kind
}

func (element *element) Owner() string {
	if element == nil || element.qualifiedName == "/" {
		return ""
	}
	return path.Dir(element.qualifiedName)
}

func (element *element) Id() string {
	if element == nil {
		return ""
	}
	return element.id
}

func (element *element) Name() string {
	if element == nil {
		return ""
	}
	return path.Base(element.qualifiedName)
}

func (element *element) QualifiedName() string {
	if element == nil {
		return ""
	}
	return element.qualifiedName
}

/******* Model *******/

// Element represents a named element in the state machine hierarchy.
// It provides basic identification and naming capabilities.
type Element = elements.NamedElement

// Model represents the complete state machine model definition.
// It contains the root state and maintains a namespace of all elements.
type Model struct {
	state
	namespace map[string]elements.NamedElement
	elements  []RedefinableElement
}

func (model *Model) Namespace() map[string]elements.NamedElement {
	return model.namespace
}

func (model *Model) push(partial RedefinableElement) {
	model.elements = append(model.elements, partial)
}

// RedefinableElement is a function type that modifies a Model by adding or updating elements.
// It's used to build the state machine structure in a declarative way.
type RedefinableElement = func(model *Model, stack []elements.NamedElement) elements.NamedElement

/******* Vertex *******/

type vertex struct {
	element
	transitions []string
}

func (vertex *vertex) Transitions() []string {
	return vertex.transitions
}

/******* State *******/

type state struct {
	vertex
	initial    string
	entry      string
	exit       string
	activities []string
	deferred   []string
}

func (state *state) Entry() string {
	return state.entry
}

func (state *state) Activities() []string {
	return state.activities
}

func (state *state) Exit() string {
	return state.exit
}

/******* Transition *******/

type paths struct {
	enter []string
	exit  []string
}

type transition struct {
	element
	source string
	target string
	guard  string
	effect string
	events []Event
	paths  map[string]paths
}

func (transition *transition) Guard() string {
	return transition.guard
}

func (transition *transition) Effect() string {
	return transition.effect
}

func (transition *transition) Events() []Event {
	return transition.events
}

func (transition *transition) Source() string {
	return transition.source
}

func (transition *transition) Target() string {
	return transition.target
}

/******* Behavior *******/

type behavior[T Context] struct {
	element
	method func(ctx context.Context, hsm T, event Event)
}

/******* Constraint *******/

type constraint[T Context] struct {
	element
	expression func(ctx context.Context, hsm T, event Event) bool
}

/******* Events *******/

// Event represents a trigger that can cause state transitions in the state machine.
// Events can carry data and have completion tracking through the Done channel.
type Event = elements.Event

var InitialEvent = Event{}
var ErrorEvent = Event{
	Kind: kind.ErrorEvent,
}

type DecodedEvent[T any] struct {
	Event
	Data T
}

func DecodeEvent[T any](event Event) (DecodedEvent[T], bool) {
	data, ok := event.Data.(T)
	return DecodedEvent[T]{
		Event: event,
		Data:  data,
	}, ok
}

var closedChannel = func() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

var noevent = Event{
	Done: closedChannel,
}

type queue struct {
	mutex  sync.RWMutex
	events []Event
}

// func (q *queue) len() int {
// 	q.mutex.RLock()
// 	defer q.mutex.RUnlock()
// 	return len(q.events)
// }

func (q *queue) pop() (Event, bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if len(q.events) == 0 {
		return noevent, false
	}
	event := q.events[0]
	q.events = q.events[1:]
	return event, true
}

func (q *queue) push(events ...Event) {
	for _, event := range events {
		if kind.IsKind(event.Kind, kind.CompletionEvent) {
			q.mutex.Lock()
			q.events = append([]Event{event}, q.events...)
			q.mutex.Unlock()
		} else {
			q.mutex.Lock()
			q.events = append(q.events, event)
			q.mutex.Unlock()
		}
	}
}

func apply(model *Model, stack []elements.NamedElement, partials ...RedefinableElement) {
	for _, partial := range partials {
		partial(model, stack)
	}
}

// Define creates a new state machine model with the given name and elements.
// The first argument can be either a string name or a RedefinableElement.
// Additional elements are added to the model in the order they are specified.
//
// Example:
//
//	model := hsm.Define(
//	    "traffic_light",
//	    hsm.State("red"),
//	    hsm.State("yellow"),
//	    hsm.State("green"),
//	    hsm.Initial("red")
//	)
func Define[T interface{ RedefinableElement | string }](nameOrRedefinableElement T, redefinableElements ...RedefinableElement) Model {
	name := "/"
	switch any(nameOrRedefinableElement).(type) {
	case string:
		name = path.Join(name, any(nameOrRedefinableElement).(string))
	case RedefinableElement:
		redefinableElements = append([]RedefinableElement{any(nameOrRedefinableElement).(RedefinableElement)}, redefinableElements...)
	}
	model := Model{
		state: state{
			vertex: vertex{element: element{kind: kind.State, qualifiedName: "/", id: name}, transitions: []string{}},
		},
		elements: redefinableElements,
	}
	model.namespace = map[string]elements.NamedElement{
		"/": &model.state,
	}
	stack := []elements.NamedElement{&model.state}
	for len(model.elements) > 0 {
		elements := model.elements
		model.elements = []RedefinableElement{}
		apply(&model, stack, elements...)
	}

	if model.initial == "" {
		panic(fmt.Errorf("initial state is required for state machine %s", model.Id()))
	}
	if model.entry != "" {
		panic(fmt.Errorf("entry actions are not allowed on top level state machine %s", model.Id()))
	}
	if model.exit != "" {
		panic(fmt.Errorf("exit actions are not allowed on top level state machine %s", model.Id()))
	}
	return model
}

func find(stack []elements.NamedElement, maybeKinds ...uint64) elements.NamedElement {
	for i := len(stack) - 1; i >= 0; i-- {
		if kind.IsKind(stack[i].Kind(), maybeKinds...) {
			return stack[i]
		}
	}
	return nil
}

func traceback(maybeError ...error) func(err error) {
	_, file, line, _ := runtime.Caller(2)
	fn := func(err error) {
		panic(fmt.Sprintf("%s:%d: %v", file, line, err))
	}
	if len(maybeError) > 0 {
		fn(maybeError[0])
	}
	return fn
}

func get[T elements.NamedElement](model *Model, name string) T {
	var zero T
	if name == "" {
		return zero
	}
	if element, ok := model.namespace[name]; ok {
		typed, ok := element.(T)
		if ok {
			return typed
		}
	}
	return zero
}

var counter atomic.Uint64

const (
	idTimestampBits = 48
	idCounterBits   = 16 // 64 - timestampBits
	idTimestampMask = uint64((1 << idTimestampBits) - 1)
	idCounterMask   = uint64((1 << idCounterBits) - 1)
)

// id generates a uint64 ID with configurable bits for timestamp
// timestampBits must be between 1 and 63
func id() string {
	// Get timestamp and truncate to specified bits
	timestamp := uint64(time.Now().UnixMilli()) & idTimestampMask
	// Get count and truncate to remaining bits
	counter := counter.Add(1) & idCounterMask

	// Combine timestamp and counter
	return strconv.FormatUint((timestamp<<idCounterBits)|counter, 32)
}

func hasWildcard(events ...Event) bool {
	for _, event := range events {
		if strings.Contains(event.Name, "*") {
			return true
		}
	}
	return false
}

// State creates a new state element with the given name and optional child elements.
// States can have entry/exit actions, activities, and transitions.
//
// Example:
//
//	hsm.State("active",
//	    hsm.Entry(func(ctx context.Context, hsm *MyHSM, event Event) {
//	        log.Println("Entering active state")
//	    }),
//	    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event Event) {
//	        // Long-running activity
//	    }),
//	    hsm.Exit(func(ctx context.Context, hsm *MyHSM, event Event) {
//	        log.Println("Exiting active state")
//	    })
//	)
func State(name string, partialElements ...RedefinableElement) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.StateMachine, kind.State)
		if owner == nil {
			traceback(fmt.Errorf("state \"%s\" must be called within Define() or State()", name))
		}
		element := &state{
			vertex: vertex{element: element{kind: kind.State, qualifiedName: path.Join(owner.QualifiedName(), name)}, transitions: []string{}},
		}
		model.namespace[element.QualifiedName()] = element
		stack = append(stack, element)
		apply(model, stack, partialElements...)
		model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
			// Sort transitions so wildcard events are at the end
			sort.SliceStable(element.transitions, func(i, j int) bool {
				transitionI := get[*transition](model, element.transitions[i])
				if transitionI == nil {
					traceback(fmt.Errorf("missing transition \"%s\" for state \"%s\"", element.transitions[i], element.QualifiedName()))
					return false
				}
				transitionJ := get[*transition](model, element.transitions[j])
				if transitionJ == nil {
					traceback(fmt.Errorf("missing transition \"%s\" for state \"%s\"", element.transitions[j], element.QualifiedName()))
					return false // because the linter doesn't know that traceback will panic
				}
				// If j has wildcard and i doesn't, i comes first
				hasWildcardI := hasWildcard(transitionI.events...)
				hasWildcardJ := hasWildcard(transitionJ.events...)
				return !hasWildcardI && hasWildcardJ
			})
			return element
		})
		return element
	}
}

// LCA finds the Lowest Common Ancestor between two qualified state names in a hierarchical state machine.
// It takes two qualified names 'a' and 'b' as strings and returns their closest common ancestor.
//
// For example:
// - LCA("/s/s1", "/s/s2") returns "/s"
// - LCA("/s/s1", "/s/s1/s11") returns "/s/s1"
// - LCA("/s/s1", "/s/s1") returns "/s/s1"
func LCA(a, b string) string {
	// if both are the same the lca is the parent
	if a == b {
		return path.Dir(a)
	}
	// if one is empty the lca is the other
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	// if the parents are the same the lca is the parent
	if path.Dir(a) == path.Dir(b) {
		return path.Dir(a)
	}
	// if a is an ancestor of b the lca is a
	if IsAncestor(a, b) {
		return a
	}
	// if b is an ancestor of a the lca is b
	if IsAncestor(b, a) {
		return b
	}
	// otherwise the lca is the lca of the parents
	return LCA(path.Dir(a), path.Dir(b))
}

func IsAncestor(current, target string) bool {
	current = path.Clean(current)
	target = path.Clean(target)
	if current == target || current == "." || target == "." {
		return false
	}
	if current == "/" {
		return true
	}
	parent := path.Dir(target)
	for parent != "/" {
		if parent == current {
			return true
		}
		parent = path.Dir(parent)
	}
	return false
}

// Transition creates a new transition between states.
// Transitions can have triggers, guards, and effects.
//
// Example:
//
//	hsm.Transition(
//	    hsm.Trigger("submit"),
//	    hsm.Source("draft"),
//	    hsm.Target("review"),
//	    hsm.Guard(func(ctx context.Context, hsm *MyHSM, event Event) bool {
//	        return hsm.IsValid()
//	    }),
//	    hsm.Effect(func(ctx context.Context, hsm *MyHSM, event Event) {
//	        log.Println("Transitioning from draft to review")
//	    })
//	)
func Transition[T interface{ RedefinableElement | string }](nameOrPartialElement T, partialElements ...RedefinableElement) RedefinableElement {
	name := ""
	switch any(nameOrPartialElement).(type) {
	case string:
		name = any(nameOrPartialElement).(string)
	case RedefinableElement:
		partialElements = append([]RedefinableElement{any(nameOrPartialElement).(RedefinableElement)}, partialElements...)
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Vertex)
		if name == "" {
			name = fmt.Sprintf("transition_%d", len(model.namespace))
		}
		if owner == nil {
			traceback(fmt.Errorf("transition \"%s\" must be called within a State() or Define()", name))
		}
		transition := &transition{
			events: []Event{},
			element: element{
				kind:          kind.Transition,
				qualifiedName: path.Join(owner.QualifiedName(), name),
			},
			source: ".",
			paths:  map[string]paths{},
		}
		model.namespace[transition.QualifiedName()] = transition
		stack = append(stack, transition)
		apply(model, stack, partialElements...)
		if transition.source == "." || transition.source == "" {
			transition.source = owner.QualifiedName()
		}
		sourceElement, ok := model.namespace[transition.source]
		if !ok {
			traceback(fmt.Errorf("missing source \"%s\" for transition \"%s\"", transition.source, transition.QualifiedName()))
		}
		switch source := sourceElement.(type) {
		case *state:
			source.transitions = append(source.transitions, transition.QualifiedName())
		case *vertex:
			source.transitions = append(source.transitions, transition.QualifiedName())
		}
		if len(transition.events) == 0 && !kind.IsKind(sourceElement.Kind(), kind.Pseudostate) {

			// TODO: completion transition
			// qualifiedName := path.Join(transition.source, ".completion")
			// transition.events = append(transition.events, &event{
			// 	element: element{kind: kind.CompletionEvent, qualifiedName: qualifiedName},
			// })
			traceback(fmt.Errorf("completion transition not implemented"))
		}
		if transition.target == transition.source {
			transition.kind = kind.Self
		} else if transition.target == "" {
			transition.kind = kind.Internal
		} else if IsAncestor(transition.source, transition.target) {
			transition.kind = kind.Local
		} else {
			transition.kind = kind.External
		}
		enter := []string{}
		entering := transition.target
		lca := LCA(transition.source, transition.target)
		for entering != lca && entering != "/" && entering != "" {
			enter = append([]string{entering}, enter...)
			entering = path.Dir(entering)
		}
		if kind.IsKind(transition.kind, kind.Self) {
			enter = append(enter, sourceElement.QualifiedName())
		}
		if kind.IsKind(sourceElement.Kind(), kind.Initial) {
			transition.paths[path.Dir(sourceElement.QualifiedName())] = paths{
				enter: enter,
				exit:  []string{sourceElement.QualifiedName()},
			}
		} else {
			model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
				if transition.source == model.QualifiedName() && transition.target != "" {
					traceback(fmt.Errorf("top level transitions must have a source and target, or no source and target"))
				}
				if kind.IsKind(transition.kind, kind.Internal) && transition.effect == "" {
					traceback(fmt.Errorf("internal transitions require an effect"))
				}
				// precompute transition paths for the source state and nested states
				for qualifiedName, element := range model.namespace {
					if strings.HasPrefix(qualifiedName, transition.source) && kind.IsKind(element.Kind(), kind.Vertex, kind.StateMachine) {
						exit := []string{}
						if transition.kind != kind.Internal {
							exiting := element.QualifiedName()
							for exiting != lca && exiting != "" {
								exit = append(exit, exiting)
								if exiting == "/" {
									break
								}
								exiting = path.Dir(exiting)
							}
						}
						transition.paths[element.QualifiedName()] = paths{
							enter: enter,
							exit:  exit,
						}
					}

				}
				return transition
			})
		}

		return transition
	}
}

// Source specifies the source state of a transition.
// It can be used within a Transition definition.
//
// Example:
//
//	hsm.Transition(
//	    hsm.Source("idle"),
//	    hsm.Target("running")
//	)
func Source[T interface{ RedefinableElement | string }](nameOrPartialElement T) RedefinableElement {
	// Capture the stack depth for use in traceback
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("hsm.Source() must be called within a hsm.Transition()"))
		}
		transition := owner.(*transition)
		if transition.source != "." && transition.source != "" {
			traceback(fmt.Errorf("transition \"%s\" already has a source \"%s\"", transition.QualifiedName(), transition.source))
		}
		var name string
		switch any(nameOrPartialElement).(type) {
		case string:
			name = any(nameOrPartialElement).(string)
			if !path.IsAbs(name) {
				if ancestor := find(stack, kind.State); ancestor != nil {
					name = path.Join(ancestor.QualifiedName(), name)
				}
			}
			// push a validation step to ensure the source exists after the model is built
			model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
				if _, ok := model.namespace[name]; !ok {
					traceback(fmt.Errorf("missing source \"%s\" for transition \"%s\"", name, transition.QualifiedName()))
				}
				return owner
			})
		case RedefinableElement:
			element := any(nameOrPartialElement).(RedefinableElement)(model, stack)
			if element == nil {
				traceback(fmt.Errorf("transition \"%s\" source is nil", transition.QualifiedName()))
			}
			name = element.QualifiedName()
		}
		transition.source = name
		return owner
	}
}

func Defer[T interface{ string | *Event | Event }](events ...T) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		state, ok := find(stack, kind.State).(*state)
		if !ok {
			traceback(fmt.Errorf("defer must be called within a State"))
		}
		for _, event := range events {
			switch evt := any(event).(type) {
			case string:
				state.deferred = append(state.deferred, evt)
			case *Event:
				state.deferred = append(state.deferred, evt.Name)
			case Event:
				state.deferred = append(state.deferred, evt.Name)
			default:
				traceback(fmt.Errorf("defer must be called with a string, *Event, or Event"))
			}
		}
		return state
	}
}

// Target specifies the target state of a transition.
// It can be used within a Transition definition.
//
// Example:
//
//	hsm.Transition(
//	    hsm.Source("idle"),
//	    hsm.Target("running")
//	)
func Target[T interface{ RedefinableElement | string }](nameOrPartialElement T) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("Target() must be called within Transition()"))
		}
		transition := owner.(*transition)
		if transition.target != "" {
			traceback(fmt.Errorf("transition \"%s\" already has target \"%s\"", transition.QualifiedName(), transition.target))
		}
		var qualifiedName string
		switch target := any(nameOrPartialElement).(type) {
		case string:
			qualifiedName = target
			if !path.IsAbs(qualifiedName) {
				if ancestor := find(stack, kind.State); ancestor != nil {
					qualifiedName = path.Join(ancestor.QualifiedName(), qualifiedName)
				}
			}
			// push a validation step to ensure the target exists after the model is built
			model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
				if _, exists := model.namespace[qualifiedName]; !exists {
					traceback(fmt.Errorf("missing target \"%s\" for transition \"%s\"", target, transition.QualifiedName()))
				}
				return transition
			})
		case RedefinableElement:
			targetElement := target(model, stack)
			if targetElement == nil {
				traceback(fmt.Errorf("transition \"%s\" target is nil", transition.QualifiedName()))
			}
			qualifiedName = targetElement.QualifiedName()
		}

		transition.target = qualifiedName
		return transition
	}
}

// Effect defines an action to be executed during a transition.
// The effect function is called after exiting the source state and before entering the target state.
//
// Example:
//
//	hsm.Effect(func(ctx context.Context, hsm *MyHSM, event Event) {
//	    log.Printf("Transitioning with event: %s", event.Name)
//	})
func Effect[T Context](fn func(ctx context.Context, hsm T, event Event), maybeName ...string) RedefinableElement {
	name := ".effect"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("effect must be called within a Transition"))
		}
		behavior := &behavior[T]{
			element: element{kind: kind.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
			method:  fn,
		}
		model.namespace[behavior.QualifiedName()] = behavior
		owner.(*transition).effect = behavior.QualifiedName()
		return owner
	}
}

// Guard defines a condition that must be true for a transition to be taken.
// If multiple transitions are possible, the first one with a satisfied guard is chosen.
//
// Example:
//
//	hsm.Guard(func(ctx context.Context, hsm *MyHSM, event Event) bool {
//	    return hsm.counter > 10
//	})
func Guard[T Context](fn func(ctx context.Context, hsm T, event Event) bool, maybeName ...string) RedefinableElement {
	name := ".guard"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("guard must be called within a Transition"))
		}
		constraint := &constraint[T]{
			element:    element{kind: kind.Constraint, qualifiedName: path.Join(owner.QualifiedName(), name)},
			expression: fn,
		}
		model.namespace[constraint.QualifiedName()] = constraint
		owner.(*transition).guard = constraint.QualifiedName()
		return owner
	}
}

// Initial defines the initial state for a composite state or the entire state machine.
// When a composite state is entered, its initial state is automatically entered.
//
// Example:
//
//	hsm.State("operational",
//	    hsm.State("idle"),
//	    hsm.State("running"),
//	    hsm.Initial("idle")
//	)
func Initial[T interface{ string | RedefinableElement }](elementOrName T, partialElements ...RedefinableElement) RedefinableElement {
	name := ".initial"
	switch any(elementOrName).(type) {
	case string:
		name = any(elementOrName).(string)
	case RedefinableElement:
		partialElements = append([]RedefinableElement{any(elementOrName).(RedefinableElement)}, partialElements...)
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.State)
		if owner == nil {
			traceback(fmt.Errorf("initial must be called within a State or Model"))
		}
		initial := &vertex{
			element: element{kind: kind.Initial, qualifiedName: path.Join(owner.QualifiedName(), name)},
		}
		owner.(*state).initial = initial.QualifiedName()
		if model.namespace[initial.QualifiedName()] != nil {
			traceback(fmt.Errorf("initial \"%s\" state already exists for \"%s\"", initial.QualifiedName(), owner.QualifiedName()))
		}
		model.namespace[initial.QualifiedName()] = initial
		stack = append(stack, initial)
		transition := (Transition(Source(initial.QualifiedName()), append(partialElements, Trigger(InitialEvent))...)(model, stack)).(*transition)
		// validation logic
		if transition.guard != "" {
			traceback(fmt.Errorf("initial \"%s\" cannot have a guard", initial.QualifiedName()))
		}
		if transition.events[0].Name != "" {
			traceback(fmt.Errorf("initial \"%s\" cannot have triggers", initial.QualifiedName()))
		}
		if !strings.HasPrefix(transition.target, owner.QualifiedName()) {
			traceback(fmt.Errorf("initial \"%s\" must target a nested state not \"%s\"", initial.QualifiedName(), transition.target))
		}
		if len(initial.transitions) > 1 {
			traceback(fmt.Errorf("initial \"%s\" cannot have multiple transitions %v", initial.QualifiedName(), initial.transitions))
		}
		return transition
	}
}

// Choice creates a pseudo-state that enables dynamic branching based on guard conditions.
// The first transition with a satisfied guard condition is taken.
//
// Example:
//
//	hsm.Choice(
//	    hsm.Transition(
//	        hsm.Target("approved"),
//	        hsm.Guard(func(ctx context.Context, hsm *MyHSM, event Event) bool {
//	            return hsm.score > 700
//	        })
//	    ),
//	    hsm.Transition(
//	        hsm.Target("rejected")
//	    )
//	)
func Choice[T interface{ RedefinableElement | string }](elementOrName T, partialElements ...RedefinableElement) RedefinableElement {
	name := ""
	switch any(elementOrName).(type) {
	case string:
		name = any(elementOrName).(string)
	case RedefinableElement:
		partialElements = append([]RedefinableElement{any(elementOrName).(RedefinableElement)}, partialElements...)
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.State, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("you must call Choice() within a State or Transition"))
		} else if kind.IsKind(owner.Kind(), kind.Transition) {
			transition := owner.(*transition)
			source := transition.source
			owner = model.namespace[source]
			if owner == nil {
				traceback(fmt.Errorf("transition \"%s\" targetting \"%s\" requires a source state when using Choice()", transition.QualifiedName(), transition.target))
			} else if kind.IsKind(owner.Kind(), kind.Pseudostate) {
				// pseudostates aren't a namespace, so we need to find the containing state
				owner = find(stack, kind.State)
				if owner == nil {
					traceback(fmt.Errorf("you must call Choice() within a State"))
				}
			}
		}
		if name == "" {
			name = fmt.Sprintf("choice_%d", len(model.elements))
		}
		qualifiedName := path.Join(owner.QualifiedName(), name)
		element := &vertex{
			element: element{kind: kind.Choice, qualifiedName: qualifiedName},
		}
		model.namespace[qualifiedName] = element
		stack = append(stack, element)
		apply(model, stack, partialElements...)
		if len(element.transitions) == 0 {
			traceback(fmt.Errorf("you must define at least one transition for choice \"%s\"", qualifiedName))
		}
		if defaultTransition := get[elements.Transition](model, element.transitions[len(element.transitions)-1]); defaultTransition != nil {
			if defaultTransition.Guard() != "" {
				traceback(fmt.Errorf("the last transition of choice state \"%s\" cannot have a guard", qualifiedName))
			}
		}
		return element
	}
}

// Entry defines an action to be executed when entering a state.
// The entry action is executed before any internal activities are started.
//
// Example:
//
//	hsm.Entry(func(ctx context.Context, hsm *MyHSM, event Event) {
//	    log.Printf("Entering state with event: %s", event.Name)
//	})
func Entry[T Context](fn func(ctx context.Context, hsm T, event Event), maybeName ...string) RedefinableElement {
	name := ".entry"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.State)
		if owner == nil {
			traceback(fmt.Errorf("entry must be called within a State"))
		}
		element := &behavior[T]{
			element: element{kind: kind.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
			method:  fn,
		}
		model.namespace[element.QualifiedName()] = element
		owner.(*state).entry = element.QualifiedName()
		return element
	}
}

// Activity defines a long-running action that is executed while in a state.
// The activity is started after the entry action and stopped before the exit action.
//
// Example:
//
//	hsm.Activity(func(ctx context.Context, hsm *MyHSM, event Event) {
//	    for {
//	        select {
//	        case <-ctx.Done():
//	            return
//	        case <-time.After(time.Second):
//	            log.Println("Activity tick")
//	        }
//	    }
//	})
func Activity[T Context](fn func(ctx context.Context, hsm T, event Event), maybeName ...string) RedefinableElement {
	name := ".activity"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.State).(*state)
		if !ok {
			traceback(fmt.Errorf("activity must be called within a State"))
		}

		element := &behavior[T]{
			element: element{kind: kind.Concurrent, qualifiedName: path.Join(owner.QualifiedName(), name)},
			method:  fn,
		}
		model.namespace[element.QualifiedName()] = element
		owner.activities = append(owner.activities, element.QualifiedName())
		return element
	}
}

// Exit defines an action to be executed when exiting a state.
// The exit action is executed after any internal activities are stopped.
//
// Example:
//
//	hsm.Exit(func(ctx context.Context, hsm *MyHSM, event Event) {
//	    log.Printf("Exiting state with event: %s", event.Name)
//	})
func Exit[T Context](fn func(ctx context.Context, hsm T, event Event), maybeName ...string) RedefinableElement {
	name := ".exit"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.State)
		if owner == nil {
			traceback(fmt.Errorf("exit must be called within a State"))
		}

		element := &behavior[T]{
			element: element{kind: kind.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
			method:  fn,
		}
		model.namespace[element.QualifiedName()] = element
		owner.(*state).exit = element.QualifiedName()
		return element
	}
}

// Trigger defines the events that can cause a transition.
// Multiple events can be specified for a single transition.
//
// Example:
//
//	hsm.Transition(
//	    hsm.Trigger("start", "resume"),
//	    hsm.Source("idle"),
//	    hsm.Target("running")
//	)
func Trigger[T interface{ string | *Event | Event }](events ...T) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("trigger must be called within a Transition"))
		}
		transition := owner.(*transition)
		for _, eventOrName := range events {
			switch any(eventOrName).(type) {
			case string:
				name := any(eventOrName).(string)
				transition.events = append(transition.events, Event{
					Kind: kind.Event,
					Name: name,
				})
			case Event:
				transition.events = append(transition.events, any(eventOrName).(Event))
			case *Event:
				event := any(eventOrName).(*Event)
				transition.events = append(transition.events, *event)
			}
		}
		return owner
	}
}

// After creates a time-based transition that occurs after a specified duration.
// The duration can be dynamically computed based on the state machine's context.
//
// Example:
//
//	hsm.Transition(
//	    hsm.After(func(ctx context.Context, hsm *MyHSM, event Event) time.Duration {
//	        return time.Second * 30
//	    }),
//	    hsm.Source("active"),
//	    hsm.Target("timeout")
//	)
func After[T Context](expr func(ctx context.Context, hsm T, event Event) time.Duration, maybeName ...string) RedefinableElement {
	name := ".after"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.Transition).(*transition)
		if !ok {
			traceback(fmt.Errorf("after must be called within a Transition"))
		}
		qualifiedName := path.Join(owner.QualifiedName(), strconv.Itoa(len(owner.events)), name)
		hash := crc32.ChecksumIEEE([]byte(qualifiedName))
		event := Event{
			Kind: kind.TimeEvent,
			Id:   strconv.FormatUint(uint64(hash), 32),
			Name: qualifiedName,
			Data: expr,
		}
		owner.events = append(owner.events, event)
		model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
			maybeSource, ok := model.namespace[owner.source]
			if !ok {
				traceback(fmt.Errorf("source \"%s\" for transition \"%s\" not found", owner.source, owner.QualifiedName()))
			}
			source, ok := maybeSource.(*state)
			if !ok {
				traceback(fmt.Errorf("after can only be used on transitions where the source is a State, not \"%s\"", maybeSource.QualifiedName()))
			}
			activity := &behavior[T]{
				element: element{kind: kind.Concurrent, qualifiedName: path.Join(source.QualifiedName(), "activity", qualifiedName)},
				method: func(ctx context.Context, hsm T, _ Event) {
					duration := expr(ctx, hsm, event)
					timer := time.NewTimer(duration)
					select {
					case <-timer.C:
						timer.Stop()
						hsm.Dispatch(ctx, event)
						return
					case <-ctx.Done():
						timer.Stop()
						return
					}
				},
			}
			model.namespace[activity.QualifiedName()] = activity
			source.activities = append(source.activities, activity.QualifiedName())
			return owner
		})
		return owner
	}
}

// Final creates a final state that represents the completion of a composite state or the entire state machine.
// When a final state is entered, a completion event is generated.
//
// Example:
//
//	hsm.State("process",
//	    hsm.State("working"),
//	    hsm.Final("done"),
//	    hsm.Transition(
//	        hsm.Source("working"),
//	        hsm.Target("done")
//	    )
//	)
func Final(name string) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.StateMachine, kind.State)
		if owner == nil {
			traceback(fmt.Errorf("final \"%s\" must be called within Define() or State()", name))
		}
		state := &state{
			vertex: vertex{element: element{kind: kind.FinalState, qualifiedName: path.Join(owner.QualifiedName(), name)}, transitions: []string{}},
		}
		model.namespace[state.QualifiedName()] = state
		model.push(
			func(model *Model, stack []elements.NamedElement) elements.NamedElement {
				if len(state.transitions) > 0 {
					traceback(fmt.Errorf("final state \"%s\" cannot have transitions", state.QualifiedName()))
				}
				if len(state.activities) > 0 {
					traceback(fmt.Errorf("final state \"%s\" cannot have activities", state.QualifiedName()))
				}
				if state.entry != "" {
					traceback(fmt.Errorf("final state \"%s\" cannot have an entry action", state.QualifiedName()))
				}
				if state.exit != "" {
					traceback(fmt.Errorf("final state \"%s\" cannot have an exit action", state.QualifiedName()))
				}
				return state
			},
		)
		return state
	}
}

func Submachine(name string, partialElements ...RedefinableElement) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.StateMachine)
		if owner == nil {
			traceback(fmt.Errorf("submachine \"%s\" must be called within Define()", name))
		}
		return nil
	}
}

// Context represents an active state machine instance that can process events and track state.
// It provides methods for event dispatch and state management.
type Context interface {
	Element
	subcontext
	// State returns the current state's qualified name.
	State() string
	// Dispatch sends an event to the state machine and returns a channel that closes when processing completes.
	Dispatch(ctx context.Context, event Event) <-chan struct{}
	Stop(ctx context.Context) <-chan struct{}
	start(Context)
}

// HSM is the base type that should be embedded in custom state machine types.
// It provides the core state machine functionality.
//
// Example:
//
//	type MyHSM struct {
//	    hsm.HSM
//	    counter int
//	}
type HSM struct {
	Context
}

func (hsm *HSM) start(ctx Context) {
	if hsm == nil || hsm.Context != nil {
		return
	}
	hsm.Context = ctx
	ctx.start(hsm)
}

func (hsm HSM) State() string {
	if hsm.Context == nil {
		return ""
	}
	return hsm.Context.State()
}

func (hsm HSM) Stop(ctx context.Context) <-chan struct{} {
	if hsm.Context == nil {
		return noevent.Done
	}
	return hsm.Context.Stop(ctx)
}

func (hsm HSM) Dispatch(ctx context.Context, event Event) <-chan struct{} {
	if hsm.Context == nil {
		return noevent.Done
	}

	return hsm.Context.Dispatch(ctx, event)
}

type subcontext = context.Context

type active struct {
	subcontext
	cancel  context.CancelFunc
	channel chan struct{}
}

type timeouts struct {
	terminate time.Duration
}

type hsm[T Context] struct {
	subcontext
	behavior[T]
	state      elements.NamedElement
	model      *Model
	active     map[string]*active
	queue      queue
	context    T
	trace      Trace
	timeouts   timeouts
	processing sync.Mutex
	mutex      sync.RWMutex
}

// Trace is a function type for tracing state machine execution.
// It receives the current context, a step description, and optional data.
// It returns a modified context and a completion function.
type Trace func(ctx context.Context, step string, data ...any) (context.Context, func(...any))

// Config provides configuration options for state machine initialization.
type Config struct {
	// Trace is a function that receives state machine execution events for debugging or monitoring.
	Trace Trace
	// Id is a unique identifier for the state machine instance.
	Id string
	// TerminateTimeout is the timeout for the state activity to terminate.
	TerminateTimeout time.Duration
	// Name is the name of the state machine.
	Name string
}

type key[T any] struct{}

var Keys = struct {
	All key[*sync.Map]
	HSM key[HSM]
}{
	All: key[*sync.Map]{},
	HSM: key[HSM]{},
}

func Match(state string, patterns ...string) bool {
	for _, pattern := range patterns {
		var lastErotemeCluster byte
		var patternIndex, sIndex, lastStar, lastEroteme int
		patternLen := len(pattern)
		sLen := len(state)
		star := -1
		eroteme := -1

	Loop:
		if sIndex >= sLen {
			goto checkPattern
		}

		if patternIndex >= patternLen {
			if star != -1 {
				patternIndex = star + 1
				lastStar++
				sIndex = lastStar
				goto Loop
			}
			continue
		}
		switch pattern[patternIndex] {
		case '.':
			// It matches any single character. So, we don't need to check anything.
		case '?':
			// '?' matches one character. Store its position and match exactly one character in the string.
			eroteme = patternIndex
			lastEroteme = sIndex
			lastErotemeCluster = byte(state[sIndex])
		case '*':
			// '*' matches zero or more characters. Store its position and increment the pattern index.
			star = patternIndex
			lastStar = sIndex
			patternIndex++
			goto Loop
		default:
			// If the characters don't match, check if there was a previous '?' or '*' to backtrack.
			if pattern[patternIndex] != state[sIndex] {
				if eroteme != -1 {
					patternIndex = eroteme + 1
					sIndex = lastEroteme
					eroteme = -1
					goto Loop
				}

				if star != -1 {
					patternIndex = star + 1
					lastStar++
					sIndex = lastStar
					goto Loop
				}

				continue
			}

			// If the characters match, check if it was not the same to validate the eroteme.
			if eroteme != -1 && lastErotemeCluster != byte(state[sIndex]) {
				eroteme = -1
			}
		}

		patternIndex++
		sIndex++
		goto Loop

		// Check if the remaining pattern characters are '*' or '?', which can match the end of the string.
	checkPattern:
		if patternIndex < patternLen {
			if pattern[patternIndex] == '*' {
				patternIndex++
				goto checkPattern
			} else if pattern[patternIndex] == '?' {
				if sIndex >= sLen {
					sIndex--
				}
				patternIndex++
				goto checkPattern
			}
		}

		if patternIndex == patternLen {
			return true
		}
	}
	return false

}

// Start creates and starts a new state machine instance with the given model and configuration.
// The state machine will begin executing from its initial state.
//
// Example:
//
//	model := hsm.Define(...)
//	sm := hsm.Start(context.Background(), &MyHSM{}, &model, hsm.Config{
//	    Trace: func(ctx context.Context, step string, data ...any) (context.Context, func(...any)) {
//	        log.Printf("Step: %s, Data: %v", step, data)
//	        return ctx, func(...any) {}
//	    },
//	    Id: "my-hsm-1",
//	})
func Start[T Context](ctx context.Context, sm T, model *Model, config ...Config) T {
	hsm := &hsm[T]{
		behavior: behavior[T]{
			element: element{
				kind: kind.StateMachine,
			},
		},
		model:      model,
		state:      &model.state,
		active:     map[string]*active{},
		context:    sm,
		queue:      queue{},
		subcontext: ctx,
	}
	hsm.processing.Lock()
	if len(config) > 0 {
		hsm.trace = config[0].Trace
		hsm.id = config[0].Id
		hsm.timeouts.terminate = config[0].TerminateTimeout
		hsm.qualifiedName = config[0].Name
	}
	if hsm.id == "" {
		hsm.id = id()
	}
	if hsm.qualifiedName == "" {
		hsm.qualifiedName = model.QualifiedName()
	}
	if hsm.timeouts.terminate == 0 {
		hsm.timeouts.terminate = time.Millisecond
	}
	hsm.method = func(ctx context.Context, _ T, event Event) {
		hsm.state = hsm.enter(ctx, hsm.state, event, true)
		hsm.process(ctx)
	}
	sm.start(hsm)
	return sm
}

func (sm *hsm[T]) State() string {
	if sm == nil {
		return ""
	}
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	if sm.state == nil {
		return ""
	}
	return sm.state.QualifiedName()
}

func (sm *hsm[T]) start(active Context) {
	all, ok := active.Value(Keys.All).(*sync.Map)
	if !ok {
		all = &sync.Map{}
	}
	subcontext := sm.activate(context.WithValue(context.WithValue(sm.subcontext, Keys.All, all), Keys.HSM, sm), sm)
	sm.subcontext = subcontext
	all.Store(sm.id, sm)
	sm.execute(sm.subcontext, &sm.behavior, InitialEvent)
	subcontext.channel <- struct{}{}
}

func (sm *hsm[T]) Stop(ctx context.Context) <-chan struct{} {
	if sm == nil {
		return closedChannel
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(ctx, "stop", sm.state)
		defer end()
	}
	done := make(chan struct{})
	go func() {
		sm.processing.Lock()

		var ok bool
		for sm.state != nil {
			select {
			case <-ctx.Done():
				return
			default:
				sm.exit(ctx, sm.state, noevent)
				sm.state, ok = sm.model.namespace[sm.state.Owner()]
				if ok {
					continue
				}
			}
			break
		}
		if all, ok := sm.Value(Keys.All).(*sync.Map); ok {
			all.Delete(sm.id)
		}
		close(done)
		sm.processing.Unlock()
	}()
	return done
}

// Stop gracefully stops a state machine instance.
// It cancels any running activities and prevents further event processing.
//
// Example:
//
//	sm := hsm.Start(...)
//	// ... use state machine ...
//	hsm.Stop(sm)
func Stop(ctx context.Context) {
	hsm, ok := FromContext(ctx)
	if !ok {
		return
	}
	hsm.Stop(ctx)
}

func (sm *hsm[T]) activate(ctx context.Context, element elements.NamedElement) *active {
	if element == nil {
		return nil
	}
	qualifiedName := element.QualifiedName()
	current, ok := sm.active[qualifiedName]
	if !ok {
		current = &active{
			channel: make(chan struct{}, 1),
		}
		sm.active[qualifiedName] = current
	}
	current.subcontext, current.cancel = context.WithCancel(context.WithValue(ctx, &sm.active, element))
	return current
}

func (sm *hsm[T]) enter(ctx context.Context, element elements.NamedElement, event Event, defaultEntry bool) elements.NamedElement {
	if sm == nil {
		return nil
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(sm, "enter", element)
		defer end()
	}
	switch element.Kind() {
	case kind.State:
		state := element.(*state)
		if entry := get[*behavior[T]](sm.model, state.entry); entry != nil {
			sm.execute(ctx, entry, event)
		}
		for _, activity := range state.activities {
			if activity := get[*behavior[T]](sm.model, activity); activity != nil {
				sm.execute(ctx, activity, event)
			}
		}
		if !defaultEntry || state.initial == "" {
			return state
		}
		return sm.initial(ctx, state, event)
	case kind.Choice:
		vertex := element.(*vertex)
		for _, qualifiedName := range vertex.transitions {
			if transition := get[*transition](sm.model, qualifiedName); transition != nil {
				if constraint := get[*constraint[T]](sm.model, transition.Guard()); constraint != nil {
					if !sm.evaluate(ctx, constraint, event) {
						continue
					}
				}
				return sm.transition(ctx, element, transition, event)
			}
		}
	case kind.FinalState:
		if element.Owner() == "/" {
			sm.terminate(ctx, sm)
		}
		return element
	}
	return nil
}

func (sm *hsm[T]) initial(ctx context.Context, state *state, event Event) elements.NamedElement {
	if sm == nil || state == nil {
		return nil
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(sm, "initial", state)
		defer end()
	}

	if initial := get[*vertex](sm.model, state.initial); initial != nil {
		if len(initial.transitions) > 0 {
			if transition := get[*transition](sm.model, initial.transitions[0]); transition != nil {
				return sm.transition(ctx, state, transition, event)
			}
		}
	}
	return state
}

func (sm *hsm[T]) exit(ctx context.Context, element elements.NamedElement, event Event) {
	if sm == nil || element == nil {
		return
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(sm, "exit", element)
		defer end()
	}
	if state, ok := element.(*state); ok {
		for _, activity := range state.activities {
			if activity := get[*behavior[T]](sm.model, activity); activity != nil {
				sm.terminate(ctx, activity)
			}
		}
		if exit := get[*behavior[T]](sm.model, state.exit); exit != nil {
			sm.execute(ctx, exit, event)
		}
	}

}

func (sm *hsm[T]) execute(ctx context.Context, element *behavior[T], event Event) {
	if sm == nil || element == nil {
		return
	}
	var end func(...any)
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(ctx, "execute", element, event)
		defer end()
	}
	switch element.Kind() {
	case kind.Concurrent:
		ctx := sm.activate(sm.subcontext, element)
		go func(ctx *active, end func(...any)) {
			if end != nil {
				defer end()
			}
			element.method(ctx, sm.context, event)
			ctx.channel <- struct{}{}
		}(ctx, end)
	default:
		element.method(ctx, sm.context, event)
	}

}

func (sm *hsm[T]) evaluate(ctx context.Context, guard *constraint[T], event Event) bool {
	if sm == nil || guard == nil || guard.expression == nil {
		return true
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(ctx, "evaluate", guard, event)
		defer end()
	}
	return guard.expression(
		ctx,
		sm.context,
		event,
	)
}

func (sm *hsm[T]) transition(ctx context.Context, current elements.NamedElement, transition *transition, event Event) elements.NamedElement {
	if sm == nil {
		return nil
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(ctx, "transition", current, transition, event)
		defer end()
	}
	path, ok := transition.paths[current.QualifiedName()]
	if !ok {
		return nil
	}
	for _, exiting := range path.exit {
		current, ok = sm.model.namespace[exiting]
		if !ok {
			return nil
		}
		sm.exit(ctx, current, event)
	}
	if effect := get[*behavior[T]](sm.model, transition.effect); effect != nil {
		sm.execute(ctx, effect, event)
	}
	if kind.IsKind(transition.kind, kind.Internal) {
		return current
	}
	for _, entering := range path.enter {
		next, ok := sm.model.namespace[entering]
		if !ok {
			return nil
		}
		defaultEntry := entering == transition.target
		current = sm.enter(ctx, next, event, defaultEntry)
		if defaultEntry {
			return current
		}
	}
	current, ok = sm.model.namespace[transition.target]
	if !ok {
		return nil
	}
	return current
}

func (sm *hsm[T]) terminate(ctx context.Context, element elements.NamedElement) {
	if sm == nil || element == nil {
		return
	}
	if sm.trace != nil {
		_, end := sm.trace(ctx, "terminate", element)
		defer end()
	}
	active, ok := sm.active[element.QualifiedName()]
	if !ok {
		return
	}
	active.cancel()
	select {
	case <-active.channel:
	case <-time.After(sm.timeouts.terminate):
		slog.Error("terminate timeout", "behavior", element.QualifiedName())
	}

}

func (sm *hsm[T]) enabled(ctx context.Context, source elements.Vertex, event Event) *transition {
	if sm == nil {
		return nil
	}
	for _, transitionQualifiedName := range source.Transitions() {
		transition := get[*transition](sm.model, transitionQualifiedName)
		if transition == nil {
			continue
		}
		for _, evt := range transition.Events() {
			if matched, err := path.Match(evt.Name, event.Name); err != nil || !matched {
				continue
			}
			if guard := get[*constraint[T]](sm.model, transition.Guard()); guard != nil {
				if !sm.evaluate(ctx, guard, event) {
					continue
				}
			}
			return transition
		}
	}
	return nil
}

func (sm *hsm[T]) process(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Default().Error("panic in state machine",
				"error", r,
				"stacktrace", string(debug.Stack()),
				"state", sm.state.QualifiedName())
			go sm.Dispatch(ctx, ErrorEvent)
		}
	}()
	if sm == nil {
		return
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(ctx, "process")
		defer end()
	}
	var deferred []Event
	event, ok := sm.queue.pop()
	for ok {
		qualifiedName := sm.state.QualifiedName()
		for qualifiedName != "" {
			source := get[*state](sm.model, qualifiedName)
			if source == nil {
				break
			}
			if transition := sm.enabled(ctx, source, event); transition != nil {
				state := sm.transition(ctx, sm.state, transition, event)
				sm.mutex.Lock()
				sm.state = state
				sm.mutex.Unlock()
				break
			}
			if matches := Match(event.Name, source.deferred...); matches {
				deferred = append(deferred, event)
				break
			}
			qualifiedName = source.Owner()
		}
		done(event.Done)
		event, ok = sm.queue.pop()
	}
	sm.queue.push(deferred...)
	sm.processing.Unlock()
}

func (sm *hsm[T]) Dispatch(ctx context.Context, event Event) <-chan struct{} {
	if sm == nil {
		return done(event.Done)
	}
	if sm.state == nil {
		return done(event.Done)
	}
	if event.Kind == 0 {
		event.Kind = kind.Event
	}
	if event.Done == nil {
		event.Done = closedChannel
	}
	if sm.trace != nil {
		var end func(...any)
		ctx, end = sm.trace(ctx, "dispatch", event)
		defer end()
	}
	sm.queue.push(event)
	if sm.processing.TryLock() {
		go sm.process(ctx)
		return event.Done
	}
	return event.Done
}

// Dispatch sends an event to a specific state machine instance.
// Returns a channel that closes when the event has been fully processed.
//
// Example:
//
//	sm := hsm.Start(...)
//	done := sm.Dispatch(hsm.Event{Name: "start"})
//	<-done // Wait for event processing to complete
func Dispatch(ctx context.Context, event Event) <-chan struct{} {
	if hsm, ok := FromContext(ctx); ok {
		return hsm.Dispatch(ctx, event)
	}
	return done(event.Done)
}

// DispatchAll sends an event to all state machine instances in the current context.
// Returns a channel that closes when all instances have processed the event.
//
// Example:
//
//	sm1 := hsm.Start(...)
//	sm2 := hsm.Start(...)
//	done := hsm.DispatchAll(context.Background(), hsm.Event{Name: "globalEvent"})
//	<-done // Wait for all instances to process the event
func DispatchAll(ctx context.Context, event Event) <-chan struct{} {
	all, ok := ctx.Value(Keys.All).(*sync.Map)
	if !ok {
		return done(event.Done)
	}
	all.Range(func(_ any, value any) bool {
		maybeSM, ok := value.(Context)
		if !ok {
			return true
		}
		maybeSM.Dispatch(ctx, event)
		return true
	})
	return event.Done
}

func DispatchTo(ctx context.Context, id string, event Event) <-chan struct{} {
	all, ok := ctx.Value(Keys.All).(*sync.Map)
	if !ok {
		return done(event.Done)
	}
	hsm, ok := all.Load(id)
	if !ok {
		return done(event.Done)
	}
	maybeSM, ok := hsm.(Context)
	if !ok {
		return done(event.Done)
	}
	return maybeSM.Dispatch(ctx, event)
}

// FromContext retrieves a state machine instance from a context.
// Returns the instance and a boolean indicating whether it was found.
//
// Example:
//
//	if sm, ok := hsm.FromContext(ctx); ok {
//	    log.Printf("Current state: %s", sm.State())
//	}
func FromContext(ctx context.Context) (Context, bool) {
	hsm, ok := ctx.Value(Keys.HSM).(Context)
	if ok {
		return hsm, true
	}
	return nil, false
}

func done(channel chan struct{}) <-chan struct{} {
	if channel == nil {
		return noevent.Done
	}
	select {
	case _, ok := <-channel:
		if !ok {
			// Channel is closed, return it without modification
			return channel
		}
	default:
		// Channel is open, try to send a value
		select {
		case channel <- struct{}{}:
			return channel
			// Successfully sent completion signal
		default:
			// Channel already has a value
		}
	}
	close(channel)
	return channel
}
