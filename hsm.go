package hsm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"reflect"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runpod/hsm/v2/elements"
	"github.com/runpod/hsm/v2/kind"
	"github.com/runpod/hsm/v2/muid"
)

var (
	Kinds               = kind.Kinds()
	EventKind           = kind.Event
	CompletionEventKind = kind.CompletionEvent
	ErrorEventKind      = kind.ErrorEvent
	ErrNilHSM           = errors.New("hsm is nil")
	ErrInvalidState     = errors.New("invalid state")
	ErrMissingHSM       = errors.New("missing hsm in context")
	ErrInvalidPattern   = errors.New("invalid pattern")
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
	element
	state    state
	members  map[string]elements.NamedElement
	elements []RedefinableElement
}

func (model *Model) Members() map[string]elements.NamedElement {
	return model.members
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
	entry      []string
	exit       []string
	activities []string
	deferred   []string
}

func (state *state) Entry() []string {
	return state.entry
}

func (state *state) Activities() []string {
	return state.activities
}

func (state *state) Exit() []string {
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
	effect []string
	events []string
	paths  map[string]paths
}

func (transition *transition) Guard() string {
	return transition.guard
}

func (transition *transition) Effect() []string {
	return transition.effect
}

func (transition *transition) Events() []string {
	return transition.events
}

func (transition *transition) Source() string {
	return transition.source
}

func (transition *transition) Target() string {
	return transition.target
}

/******* Behavior *******/

type Operation[T Instance] func(ctx context.Context, hsm T, event Event)
type Expression[T Instance] func(ctx context.Context, hsm T, event Event) bool

type behavior[T Instance] struct {
	element
	operation Operation[T]
}

/******* Constraint *******/

type constraint[T Instance] struct {
	element
	expression Expression[T]
}

/******* Events *******/

// Event represents a trigger that can cause state transitions in the state machine.
// Events can carry data and have completion tracking through the Done channel.
type Event = elements.Event

var (
	InitialEvent = Event{
		Name: "hsm_initial",
		Kind: kind.CompletionEvent,
	}
	ErrorEvent = Event{
		Name: "hsm_error",
		Kind: kind.ErrorEvent,
	}
	AnyEvent = Event{
		Name: "*",
		Kind: kind.Event,
	}
	FinalEvent = Event{
		Name: "hsm_final",
		Kind: kind.CompletionEvent,
	}
	InfiniteDuration = time.Duration(-1)
)

var closedChannel = func() chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}()

type queue struct {
	mutex            sync.RWMutex
	completionEvents []Event // lifo
	events           []Event // fifo
}

var empty = Event{}

func (q *queue) len() int {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	return len(q.events) + len(q.completionEvents)
}

func (q *queue) pop() (Event, bool) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	switch {
	case len(q.completionEvents) > 0:
		event := q.completionEvents[len(q.completionEvents)-1]
		q.completionEvents = q.completionEvents[:len(q.completionEvents)-1]
		return event, true
	case len(q.events) > 0:
		event := q.events[0]
		q.events = q.events[1:]
		return event, true
	default:
		return empty, false
	}
}

func (q *queue) push(events ...Event) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	for _, event := range events {
		if kind.IsKind(event.Kind, kind.CompletionEvent) {
			q.completionEvents = append(q.completionEvents, event)
		} else {
			q.events = append(q.events, event)
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
		element: element{kind: kind.Namespace, qualifiedName: "/", id: name},
		state: state{
			vertex: vertex{element: element{kind: kind.State, qualifiedName: "/", id: name}, transitions: []string{}},
		},
		elements: redefinableElements,
	}
	model.members = map[string]elements.NamedElement{
		"/": &model.state,
	}
	stack := []elements.NamedElement{&model.state}
	for len(model.elements) > 0 {
		elements := model.elements
		model.elements = []RedefinableElement{}
		apply(&model, stack, elements...)
	}

	if model.state.initial == "" {
		panic(fmt.Errorf("initial state is required for state machine %s", model.state.id))
	}
	if len(model.state.entry) > 0 {
		panic(fmt.Errorf("entry actions are not allowed on top level state machine %s", model.state.id))
	}
	if len(model.state.exit) > 0 {
		panic(fmt.Errorf("exit actions are not allowed on top level state machine %s", model.state.id))
	}
	model.qualifiedName = name
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
	if element, ok := model.members[name]; ok {
		typed, ok := element.(T)
		if ok {
			return typed
		}
	}
	return zero
}

func getFunctionName(fn any) string {
	if fn == nil {
		return ""
	}
	return path.Base(runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name())
}

func hasWildcard(events ...string) bool {
	for _, event := range events {
		if strings.Contains(event, "*") {
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
		owner := find(stack, kind.Namespace)
		if owner == nil {
			traceback(fmt.Errorf("state \"%s\" must be called within Define() or State()", name))
		}
		element := &state{
			vertex: vertex{element: element{kind: kind.State, qualifiedName: path.Join(owner.QualifiedName(), name)}, transitions: []string{}},
		}
		model.members[element.QualifiedName()] = element
		stack = append(stack, element)
		apply(model, stack, partialElements...)
		model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
			// Sort transitions so wildcard events are at the end
			slices.SortStableFunc(element.transitions, func(i, j string) int {
				transitionI := get[*transition](model, i)
				if transitionI == nil {
					traceback(fmt.Errorf("missing transition \"%s\" for state \"%s\"", i, element.QualifiedName()))
					return 1 // because the linter doesn't know that traceback will panic
				}
				transitionJ := get[*transition](model, j)
				if transitionJ == nil {
					traceback(fmt.Errorf("missing transition \"%s\" for state \"%s\"", j, element.QualifiedName()))
					return 1 // because the linter doesn't know that traceback will panic
				}
				// If j has wildcard and i doesn't, i comes first
				hasWildcardI := hasWildcard(transitionI.events...)
				hasWildcardJ := hasWildcard(transitionJ.events...)
				if hasWildcardI && !hasWildcardJ {
					return 1 // Sort transitionI (wildcard) after transitionJ
				}
				if !hasWildcardI && hasWildcardJ {
					return -1 // Sort transitionI (non-wildcard) before transitionJ
				}
				return 0
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
			name = fmt.Sprintf("transition_%d", len(model.members))
		}
		if owner == nil {
			traceback(fmt.Errorf("transition \"%s\" must be called within a State() or Define()", name))
		}
		transition := &transition{
			events: []string{},
			element: element{
				kind:          kind.Transition,
				qualifiedName: path.Join(owner.QualifiedName(), name),
			},
			source: ".",
			paths:  map[string]paths{},
		}
		model.members[transition.QualifiedName()] = transition
		stack = append(stack, transition)
		apply(model, stack, partialElements...)
		if transition.source == "." || transition.source == "" {
			transition.source = owner.QualifiedName()
		}
		sourceElement, ok := model.members[transition.source]
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
				if kind.IsKind(transition.kind, kind.Internal) && len(transition.effect) == 0 {
					traceback(fmt.Errorf("internal transitions require an effect"))
				}
				// precompute transition paths for the source state and nested states
				for qualifiedName, element := range model.members {
					if strings.HasPrefix(qualifiedName, transition.source) && kind.IsKind(element.Kind(), kind.Vertex) {
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
				if _, ok := model.members[name]; !ok {
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

// Defer schedules events to be processed after the current state is exited.
//
// Example:
//
//	hsm.Defer(hsm.Event{Name: "event_name"})
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
				if _, exists := model.members[qualifiedName]; !exists {
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
func Effect[T Instance](funcs ...func(ctx context.Context, hsm T, event Event)) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.Transition).(*transition)
		if !ok {
			traceback(fmt.Errorf("effect must be called within a Transition"))
		}
		for _, fn := range funcs {
			name := getFunctionName(fn)
			behavior := &behavior[T]{
				element:   element{kind: kind.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
				operation: fn,
			}
			model.members[behavior.QualifiedName()] = behavior
			owner.effect = append(owner.effect, behavior.QualifiedName())
		}
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
func Guard[T Instance](fn func(ctx context.Context, hsm T, event Event) bool) RedefinableElement {
	name := getFunctionName(fn)
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
		model.members[constraint.QualifiedName()] = constraint
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
		if model.members[initial.QualifiedName()] != nil {
			traceback(fmt.Errorf("initial \"%s\" state already exists for \"%s\"", initial.QualifiedName(), owner.QualifiedName()))
		}
		model.members[initial.QualifiedName()] = initial
		stack = append(stack, initial)
		transition := (Transition(Source(initial.QualifiedName()), append(partialElements, On(InitialEvent))...)(model, stack)).(*transition)
		// validation logic
		if transition.guard != "" {
			traceback(fmt.Errorf("initial \"%s\" cannot have a guard", initial.QualifiedName()))
		}
		if transition.events[0] != InitialEvent.Name {
			traceback(fmt.Errorf("initial \"%s\" must not have a trigger \"%s\"", initial.QualifiedName(), InitialEvent.Name))
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
			owner = model.members[source]
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
		model.members[qualifiedName] = element
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
func Entry[T Instance](funcs ...func(ctx context.Context, hsm T, event Event)) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.State).(*state)
		if owner == nil {
			traceback(fmt.Errorf("entry must be called within a State"))
		}
		for _, fn := range funcs {
			name := getFunctionName(fn)
			element := &behavior[T]{
				element:   element{kind: kind.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
				operation: fn,
			}
			model.members[element.QualifiedName()] = element
			owner.entry = append(owner.entry, element.QualifiedName())
		}
		return owner
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
func Activity[T Instance](funcs ...func(ctx context.Context, hsm T, event Event)) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.State).(*state)
		if !ok {
			traceback(fmt.Errorf("activity must be called within a State"))
		}
		for _, fn := range funcs {
			name := getFunctionName(fn)
			element := &behavior[T]{
				element:   element{kind: kind.Concurrent, qualifiedName: path.Join(owner.QualifiedName(), name)},
				operation: fn,
			}
			model.members[element.QualifiedName()] = element
			owner.activities = append(owner.activities, element.QualifiedName())
		}
		return owner
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
func Exit[T Instance](funcs ...func(ctx context.Context, hsm T, event Event)) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.State).(*state)
		if !ok {
			traceback(fmt.Errorf("exit must be called within a State"))
		}
		for _, fn := range funcs {
			name := getFunctionName(fn)
			element := &behavior[T]{
				element:   element{kind: kind.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
				operation: fn,
			}
			model.members[element.QualifiedName()] = element
			owner.exit = append(owner.exit, element.QualifiedName())
		}
		return owner
	}
}

// On defines the events that can cause a transition.
// Multiple events can be specified for a single transition.
//
// Example:
//
//	hsm.Transition(
//	    hsm.On("start", "resume"),
//	    hsm.Source("idle"),
//	    hsm.Target("running")
//	)
func On[T interface{ string | *Event | Event }](events ...T) RedefinableElement {
	traceback := traceback()
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner := find(stack, kind.Transition)
		if owner == nil {
			traceback(fmt.Errorf("trigger must be called within a Transition"))
		}
		transition := owner.(*transition)
		for _, eventOrName := range events {
			var name string
			switch e := any(eventOrName).(type) {
			case string:
				name = e
			case Event:
				name = e.Name
			case *Event:
				name = e.Name
			}
			transition.events = append(transition.events, name)
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
func After[T Instance](expr func(ctx context.Context, hsm T, event Event) time.Duration) RedefinableElement {
	traceback := traceback()
	name := getFunctionName(expr)
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.Transition).(*transition)
		if !ok {
			traceback(fmt.Errorf("after must be called within a Transition"))
		}
		qualifiedName := path.Join(owner.QualifiedName(), name, strconv.Itoa(len(model.members)))
		// hash := crc32.ChecksumIEEE([]byte(qualifiedName))
		event := Event{
			Kind: kind.TimeEvent,
			// Id:   strconv.FormatUint(uint64(hash), 32),
			Name: qualifiedName,
		}
		owner.events = append(owner.events, qualifiedName)
		model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
			maybeSource, ok := model.members[owner.source]
			if !ok {
				traceback(fmt.Errorf("source \"%s\" for transition \"%s\" not found", owner.source, owner.QualifiedName()))
			}
			source, ok := maybeSource.(*state)
			if !ok {
				traceback(fmt.Errorf("after can only be used on transitions where the source is a State, not \"%s\"", maybeSource.QualifiedName()))
			}
			activity := &behavior[T]{
				element: element{kind: kind.Concurrent, qualifiedName: path.Join(source.QualifiedName(), "activity", qualifiedName)},
				operation: func(ctx context.Context, hsm T, _ Event) {
					duration := expr(ctx, hsm, event)
					if duration < 0 {
						return
					}
					timer := time.NewTimer(duration)
					select {
					case <-timer.C:
						timer.Stop()
						hsm.Dispatch(hsm.Context(), event)
						return
					case <-ctx.Done():
						timer.Stop()
						return
					}
				},
			}
			model.members[activity.QualifiedName()] = activity
			source.activities = append(source.activities, activity.QualifiedName())
			return owner
		})
		return owner
	}
}

// Every schedules events to be processed on an interval.
//
// Example:
//
//	hsm.Every(func(ctx context.Context, hsm T, event Event) time.Duration {
//	    return time.Second * 30
//	})
func Every[T Instance](expr func(ctx context.Context, hsm T, event Event) time.Duration) RedefinableElement {
	traceback := traceback()
	name := getFunctionName(expr)
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.Transition).(*transition)
		if !ok {
			traceback(fmt.Errorf("after must be called within a Transition"))
		}
		qualifiedName := path.Join(owner.QualifiedName(), name, strconv.Itoa(len(model.members)))
		// hash := crc32.ChecksumIEEE([]byte(qualifiedName))
		event := Event{
			Kind: kind.TimeEvent,
			// Id:   strconv.FormatUint(uint64(hash), 32),
			Name: qualifiedName,
		}
		owner.events = append(owner.events, qualifiedName)
		model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
			maybeSource, ok := model.members[owner.source]
			if !ok {
				traceback(fmt.Errorf("source \"%s\" for transition \"%s\" not found", owner.source, owner.QualifiedName()))
			}
			source, ok := maybeSource.(*state)
			if !ok {
				traceback(fmt.Errorf("Ever() can only be used on transitions where the source is a State, not \"%s\"", maybeSource.QualifiedName()))
			}
			activity := &behavior[T]{
				element: element{kind: kind.Concurrent, qualifiedName: path.Join(source.QualifiedName(), "activity", qualifiedName)},
				operation: func(ctx context.Context, hsm T, evt Event) {
					duration := expr(ctx, hsm, evt)
					if duration < 0 {
						return
					}
					timer := time.NewTimer(duration)
					defer timer.Stop()
					for {
						select {
						case <-timer.C:
							<-hsm.Dispatch(hsm.Context(), event)
							timer.Reset(duration)
						case <-ctx.Done():
							return
						}
					}
				},
			}
			model.members[activity.QualifiedName()] = activity
			source.activities = append(source.activities, activity.QualifiedName())
			return owner
		})
		return owner
	}
}

func When[T Instance](expr func(ctx context.Context, hsm T, event Event) <-chan struct{}) RedefinableElement {
	traceback := traceback()
	name := getFunctionName(expr)
	return func(model *Model, stack []elements.NamedElement) elements.NamedElement {
		owner, ok := find(stack, kind.Transition).(*transition)
		if !ok {
			traceback(fmt.Errorf("when must be called within a Transition"))
		}
		qualifiedName := path.Join(owner.QualifiedName(), name, strconv.Itoa(len(model.members)))
		event := Event{
			Kind: kind.TimeEvent,
			Name: qualifiedName,
		}
		owner.events = append(owner.events, qualifiedName)
		model.push(func(model *Model, stack []elements.NamedElement) elements.NamedElement {
			maybeSource, ok := model.members[owner.source]
			if !ok {
				traceback(fmt.Errorf("source \"%s\" for transition \"%s\" not found", owner.source, owner.QualifiedName()))
			}
			source, ok := maybeSource.(*state)
			if !ok {
				traceback(fmt.Errorf("when can only be used on transitions where the source is a State, not \"%s\"", maybeSource.QualifiedName()))
			}
			activity := &behavior[T]{
				element: element{kind: kind.Concurrent, qualifiedName: path.Join(source.QualifiedName(), "activity", qualifiedName)},
				operation: func(ctx context.Context, hsm T, _ Event) {
					ch := expr(ctx, hsm, event)
					for {
						select {
						case <-ch:
							hsm.Dispatch(hsm.Context(), event)
						case <-ctx.Done():
							return
						}
					}
				},
			}
			model.members[activity.QualifiedName()] = activity
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
		owner := find(stack, kind.Namespace)
		if owner == nil {
			traceback(fmt.Errorf("final \"%s\" must be called within Define() or State()", name))
		}
		state := &state{
			vertex: vertex{element: element{kind: kind.FinalState, qualifiedName: path.Join(owner.QualifiedName(), name)}, transitions: []string{}},
		}
		model.members[state.QualifiedName()] = state
		model.push(
			func(model *Model, stack []elements.NamedElement) elements.NamedElement {
				if len(state.transitions) > 0 {
					traceback(fmt.Errorf("final state \"%s\" cannot have transitions", state.QualifiedName()))
				}
				if len(state.activities) > 0 {
					traceback(fmt.Errorf("final state \"%s\" cannot have activities", state.QualifiedName()))
				}
				if len(state.entry) > 0 {
					traceback(fmt.Errorf("final state \"%s\" cannot have an entry action", state.QualifiedName()))
				}
				if len(state.exit) > 0 {
					traceback(fmt.Errorf("final state \"%s\" cannot have an exit action", state.QualifiedName()))
				}
				return state
			},
		)
		return state
	}
}

// Match provides a simple interface, handling basic cases directly
// and delegating complex matching to the match function.
func Match(value string, patterns ...string) bool {
	for _, pattern := range patterns {
		// fast path for exact match
		if pattern == value {
			return true
		}
		// fast path for pure wildcard match
		if pattern == "*" {
			return true
		}
		patternLen := len(pattern)
		// fast path for empty pattern
		if patternLen == 0 {
			return value == ""
		}
		// fast path for long strings with a pattern that ends with "*
		if pattern[patternLen-1] == '*' && strings.HasPrefix(value, pattern[:patternLen-1]) {
			return true
		}
		// parse the value and pattern to check for a match
		if parse(value, pattern) {
			return true
		}
	}
	return false
}

// parse implements wildcard matching using a goto-based iterative approach.
// It supports the '*' wildcard, which matches zero or more characters.
func parse(value, pattern string) bool {
	valueIndex, patternIndex := 0, 0
	valueLen, patternLen := len(value), len(pattern)
	// patternStarIndex: index of the last '*' encountered in the pattern p.
	// valueStarIndex: index in the value string v corresponding to the position *after* the characters matched by the last '*'.
	patternStarIndex, valueStarIndex := -1, -1

LOOP_START:
	// Check if the current pattern character is '*'
	if patternIndex < patternLen && pattern[patternIndex] == '*' {
		patternStarIndex = patternIndex // Remember the position of this '*'
		patternIndex++                  // Advance the pattern index past the '*'
		valueStarIndex = valueIndex     // Remember the value index where '*' matching might backtrack to
		// If '*' is the last character in the pattern, it matches the rest of the value
		if patternIndex == patternLen {
			return true
		}
		// Continue processing, effectively trying to match zero characters with '*' first
		goto LOOP_START
	}

	// Check if current characters match
	if valueIndex < valueLen && patternIndex < patternLen && pattern[patternIndex] == value[valueIndex] {
		valueIndex++    // Advance value index
		patternIndex++  // Advance pattern index
		goto LOOP_START // Continue matching the next characters
	}

	// Check if we have reached the end of both strings
	if valueIndex == valueLen && patternIndex == patternLen {
		return true // Both strings are exhausted, successful match
	}

	// Check if we reached the end of the value string, but the pattern string remains
	if valueIndex == valueLen && patternIndex < patternLen {
		// Consume any trailing '*' characters in the pattern
		for patternIndex < patternLen && pattern[patternIndex] == '*' {
			patternIndex++
		}
		// If the pattern is now exhausted, it's a match
		return patternIndex == patternLen
	}

	// Mismatch occurred, or end of pattern reached while value string still has characters.
	// Try backtracking if a '*' was previously encountered.
	if patternStarIndex != -1 {
		// Backtrack: Advance the value index associated with the last '*'
		valueStarIndex++
		// If the backtracking value index goes beyond the value string length, matching failed
		if valueStarIndex > valueLen {
			return false
		}
		valueIndex = valueStarIndex         // Reset the current value index to the new backtrack position
		patternIndex = patternStarIndex + 1 // Reset the pattern index to the character immediately after the last '*'
		goto LOOP_START                     // Retry matching from the new state
	}
	return false
}

type Snapshot struct {
	ID            string
	QualifiedName string
	State         string
	QueueLen      int
}

// Instance represents an active state machine instance that can process events and track state.
// It provides methods for event dispatch and state management.
type Instance interface {
	// State returns the current state's qualified name.
	State() string
	Context() *active
	// Dispatch sends an event to the state machine and returns a channel that closes when processing completes.
	Dispatch(ctx context.Context, event Event) <-chan struct{}

	// non exported
	channels() *after
	takeSnapshot() Snapshot
	wait() <-chan struct{}
	start(ctx context.Context, instance Instance, event *Event)
	stop(ctx context.Context) <-chan struct{}
	restart(ctx context.Context, maybeData ...any) <-chan struct{}
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

type instance = Instance

type HSM struct {
	instance
}

func (hsm *HSM) start(ctx context.Context, instance Instance, event *Event) {
	if hsm == nil || hsm.instance != nil {
		return
	}
	hsm.instance = instance
	instance.start(ctx, hsm, event)
}

type subcontext = context.Context

type active struct {
	subcontext
	context context.Context
	cancel  context.CancelFunc
	channel chan struct{}
}

type timeouts struct {
	activity time.Duration
}

type mutex struct {
	internal sync.Mutex
	signal   atomic.Value
}

func (mutex *mutex) lock() {
	mutex.internal.Lock()
	mutex.signal.Store(make(chan struct{}))
}

func (mutex *mutex) unlock() {
	mutex.internal.Unlock()
	signal := mutex.signal.Load().(chan struct{})
	close(signal)
}

func (mutex *mutex) wait() <-chan struct{} {
	signal := mutex.signal.Load().(chan struct{})
	return signal
}

func (mutex *mutex) tryLock() bool {
	if mutex.internal.TryLock() {
		mutex.signal.Store(make(chan struct{}))
		return true
	}
	return false
}

type after struct {
	entered    sync.Map
	exited     sync.Map
	dispatched sync.Map
	processed  sync.Map
	activities sync.Map
}

type hsm[T Instance] struct {
	behavior[T]
	state      atomic.Value
	context    *active
	model      *Model
	active     map[string]*active
	queue      queue
	instance   T
	timeouts   timeouts
	processing mutex
	after      after
}

// Config provides configuration options for state machine initialization.
type Config struct {
	// ID is a unique identifier for the state machine instance.
	ID string
	// ActivityTimeout is the timeout for the state activity to terminate.
	ActivityTimeout time.Duration
	// Name is the name of the state machine.
	Name string
	// Data to be passed during initialization
	Data any
}

type key[T any] struct{}

var Keys = struct {
	Instances key[*atomic.Pointer[[]Instance]]
	HSM       key[HSM]
}{
	Instances: key[*atomic.Pointer[[]Instance]]{},
	HSM:       key[HSM]{},
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
func Start[T Instance](ctx context.Context, sm T, model *Model, maybeConfig ...Config) T {
	hsm := &hsm[T]{
		behavior: behavior[T]{
			element: element{
				kind: kind.StateMachine,
			},
		},
		model:    model,
		instance: sm,
		queue:    queue{},
		active:   map[string]*active{},
		context: &active{
			context: ctx,
		},
	}
	hsm.state.Store(&model.state)
	initialEvent := InitialEvent
	hsm.processing.lock()
	if len(maybeConfig) > 0 {
		config := maybeConfig[0]
		hsm.behavior.id = config.ID
		hsm.timeouts.activity = config.ActivityTimeout
		hsm.behavior.qualifiedName = config.Name
		initialEvent = initialEvent.WithData(config.Data)
	}
	if hsm.behavior.id == "" {
		hsm.behavior.id = muid.Make().String()
	}
	if hsm.behavior.qualifiedName == "" {
		hsm.behavior.qualifiedName = model.QualifiedName()
	}
	if hsm.timeouts.activity == 0 {
		hsm.timeouts.activity = time.Millisecond
	}
	hsm.behavior.operation = func(ctx context.Context, _ T, event Event) {
		hsm.state.Store(hsm.enter(ctx, &hsm.model.state, &event, true))
		hsm.process(ctx)
	}
	sm.start(ctx, hsm, &initialEvent)
	return sm
}

func (sm *hsm[T]) State() string {
	if sm == nil {
		return ""
	}
	state := sm.state.Load().(elements.NamedElement)
	if state == nil {
		return ""
	}
	return state.QualifiedName()
}

func (sm *hsm[T]) start(ctx context.Context, instance Instance, event *Event) {
	instances, ok := ctx.Value(Keys.Instances).(*sync.Map)
	if !ok {
		instances = &sync.Map{}
	}
	sm.context.subcontext, sm.context.cancel = context.WithCancel(context.WithValue(context.WithValue(ctx, Keys.Instances, instances), Keys.HSM, sm))
	instances.Store(sm.behavior.id, sm)
	sm.execute(sm.context, &sm.behavior, event)
}

func (sm *hsm[T]) restart(ctx context.Context, maybeData ...any) <-chan struct{} {
	var data any
	if len(maybeData) > 0 {
		data = maybeData[0]
	}
	<-sm.stop(ctx)
	sm.processing.lock()
	initialEvent := InitialEvent.WithData(data)
	sm.context = &active{
		context: ctx,
	}
	(*hsm[T])(sm).start(ctx, sm, &initialEvent)
	return sm.processing.wait()
}

func (sm *hsm[T]) wait() <-chan struct{} {
	return sm.processing.wait()
}

func (sm *hsm[T]) stop(ctx context.Context) <-chan struct{} {
	if sm == nil {
		return closedChannel
	}
	signal := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Default().Error("panic in stop", "error", r)
			}
			close(signal)
		}()
		sm.processing.lock()

		var ok bool
		state := sm.state.Load().(elements.NamedElement)
		for state != nil {
			select {
			case <-ctx.Done():
				return
			default:
				sm.exit(ctx, state, &FinalEvent)
				state, ok = sm.model.members[state.Owner()]
				if ok {
					sm.state.Store(state)
					continue
				}
			}
			break
		}
		sm.context.cancel()
		clear(sm.active)
		if instances, ok := sm.context.Value(Keys.Instances).(*sync.Map); ok {
			instances.Delete(sm.behavior.id)
		}

		sm.processing.unlock()
	}()
	return signal
}

func (sm *hsm[T]) Context() *active {
	if sm == nil {
		return nil
	}
	return sm.context
}

func (sm *hsm[T]) channels() *after {
	if sm == nil {
		return nil
	}
	return &sm.after
}

func (sm *hsm[T]) activate(ctx context.Context, element elements.NamedElement) *active {
	if element == nil {
		return nil
	}
	qualifiedName := element.QualifiedName()
	maybeActive, ok := sm.active[qualifiedName]
	if !ok {
		maybeActive = &active{
			channel: make(chan struct{}, 1),
		}
		sm.active[qualifiedName] = maybeActive
	}
	maybeActive.subcontext, maybeActive.cancel = context.WithCancel(ctx)
	return maybeActive
}

func (sm *hsm[T]) executeAll(ctx context.Context, names []string, event *Event) {
	for _, qualifiedName := range names {
		if behavior := get[*behavior[T]](sm.model, qualifiedName); behavior != nil {
			sm.execute(ctx, behavior, event)
		}
	}
}

func (sm *hsm[T]) enter(ctx context.Context, element elements.NamedElement, event *Event, defaultEntry bool) elements.NamedElement {
	if sm == nil {
		return nil
	}
	switch element.Kind() {
	case kind.State:
		state := element.(*state)
		for _, entry := range state.entry {
			if entry := get[*behavior[T]](sm.model, entry); entry != nil {
				sm.execute(ctx, entry, event)
			}
		}
		if len(state.activities) > 0 {
			sm.executeAll(ctx, state.activities, event)
		}
		if !defaultEntry || state.initial == "" {
			return state
		}
		if initial := get[*vertex](sm.model, state.initial); initial != nil {
			if len(initial.transitions) > 0 {
				if transition := get[*transition](sm.model, initial.transitions[0]); transition != nil {
					return sm.transition(ctx, state, transition, event)
				}
			}
		}
		return state
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
			sm.context.cancel()
		}
		return element
	}
	return nil
}

func (sm *hsm[T]) exit(ctx context.Context, element elements.NamedElement, event *Event) {
	if sm == nil || element == nil {
		return
	}
	if state, ok := element.(*state); ok {
		// if len(state.activities) > 0 {
		// 	sm.terminateAll(ctx, state.activities)
		// }
		for _, activity := range state.activities {
			if activity := get[*behavior[T]](sm.model, activity); activity != nil {
				sm.terminate(ctx, activity)
			}
		}
		for _, exit := range state.exit {
			if exit := get[*behavior[T]](sm.model, exit); exit != nil {
				sm.execute(ctx, exit, event)
			}
		}
	}

}

func (sm *hsm[T]) execute(ctx context.Context, element *behavior[T], event *Event) {
	if sm == nil || element == nil {
		return
	}
	switch element.Kind() {
	case kind.Concurrent:
		ctx := sm.activate(ctx, element)
		go func(ctx *active, event Event) {
			defer func() {
				if r := recover(); r != nil {
					go sm.Dispatch(ctx, ErrorEvent.WithData(fmt.Errorf("panic in concurrent behavior %s: %s", element.QualifiedName(), r)))
				}
				if ch, ok := sm.after.activities.LoadAndDelete(element.QualifiedName()); ok {
					close(ch.(chan struct{}))
				}
			}()
			element.operation(ctx, sm.instance, event)
			ctx.channel <- struct{}{}
		}(ctx, *event)
	default:
		element.operation(ctx, sm.instance, *event)
	}

}

func (sm *hsm[T]) evaluate(ctx context.Context, guard *constraint[T], event *Event) bool {
	if sm == nil || guard == nil || guard.expression == nil {
		return true
	}
	return guard.expression(
		ctx,
		sm.instance,
		*event,
	)
}

func (sm *hsm[T]) transition(ctx context.Context, current elements.NamedElement, transition *transition, event *Event) elements.NamedElement {
	if sm == nil {
		return nil
	}
	path, ok := transition.paths[current.QualifiedName()]
	if !ok {
		return nil
	}
	for _, exiting := range path.exit {
		current, ok = sm.model.members[exiting]
		if !ok {
			return nil
		}
		sm.exit(ctx, current, event)
		if ch, ok := sm.after.exited.LoadAndDelete(exiting); ok {
			close(ch.(chan struct{}))
		}
	}
	for _, effect := range transition.effect {
		if effect := get[*behavior[T]](sm.model, effect); effect != nil {
			sm.execute(ctx, effect, event)
		}
	}
	if kind.IsKind(transition.kind, kind.Internal) {
		return current
	}
	for _, entering := range path.enter {
		next, ok := sm.model.members[entering]
		if !ok {
			return nil
		}
		defaultEntry := entering == transition.target
		current = sm.enter(ctx, next, event, defaultEntry)
		if ch, ok := sm.after.entered.LoadAndDelete(entering); ok {
			close(ch.(chan struct{}))
		}
		if defaultEntry {
			return current
		}
	}
	current, ok = sm.model.members[transition.target]
	if !ok {
		return nil
	}
	return current
}

func (sm *hsm[T]) terminate(ctx context.Context, element elements.NamedElement) {
	if sm == nil || element == nil {
		return
	}
	// sm.mutex.Lock()
	maybeActive, ok := sm.active[element.QualifiedName()]
	if !ok {
		// sm.mutex.Unlock()
		return
	}
	maybeActive.cancel()
	// sm.mutex.Unlock()
	select {
	case <-maybeActive.channel:
	case <-time.After(sm.timeouts.activity):
		go sm.Dispatch(ctx, ErrorEvent.WithData(fmt.Errorf("terminate timeout: %s", element.QualifiedName())))
	}

}

func (sm *hsm[T]) enabled(ctx context.Context, source elements.Vertex, event *Event) *transition {
	if sm == nil {
		return nil
	}
	for _, transitionQualifiedName := range source.Transitions() {
		transition := get[*transition](sm.model, transitionQualifiedName)
		if transition == nil {
			continue
		}
		for _, evt := range transition.Events() {
			if !Match(event.Name, evt) {
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
			err := fmt.Errorf("hsm: panic while processing event in state machine: %v\n\n%s", r, string(debug.Stack()))
			go sm.Dispatch(ctx, ErrorEvent.WithData(err))
		}
		sm.processing.unlock()
	}()
	if sm == nil {
		return
	}
	var deferred []Event
	event, ok := sm.queue.pop()
	for ok {
		if event.Id == 0 {
			event.Id = muid.Make()
		}
		currentState := sm.state.Load().(elements.NamedElement)
		qualifiedName := currentState.QualifiedName()
		for qualifiedName != "" {
			source := get[*state](sm.model, qualifiedName)
			if source == nil {
				break
			}
			if transition := sm.enabled(ctx, source, &event); transition != nil {
				state := sm.transition(ctx, currentState, transition, &event)
				if state == nil {
					break
				}
				sm.state.Store(state)
				if len(deferred) > 0 {
					sm.queue.push(deferred...)
					deferred = nil
				}
				break
			}
			if len(source.deferred) > 0 && Match(event.Name, source.deferred...) {
				deferred = append(deferred, event)
				break
			}
			qualifiedName = source.Owner()
		}
		if ch, ok := sm.after.processed.LoadAndDelete(event.Name); ok {
			close(ch.(chan struct{}))
		}
		event, ok = sm.queue.pop()
	}
	sm.queue.push(deferred...)
}

func (sm *hsm[T]) takeSnapshot() Snapshot {
	if sm == nil {
		return Snapshot{}
	}
	state, ok := sm.state.Load().(elements.NamedElement)
	if !ok {
		state = sm.model
	}
	return Snapshot{
		ID:            sm.behavior.id,
		QualifiedName: sm.behavior.qualifiedName,
		State:         state.QualifiedName(),
		QueueLen:      sm.queue.len(),
	}
}

func (sm *hsm[T]) Dispatch(ctx context.Context, event Event) <-chan struct{} {
	if sm == nil {
		return closedChannel
	}
	state := sm.state.Load().(elements.NamedElement)
	if state == nil {
		return closedChannel
	}
	if event.Kind == 0 {
		event.Kind = kind.Event
	}
	sm.queue.push(event)
	if sm.processing.tryLock() {
		go sm.process(ctx)
	}
	if ch, ok := sm.after.dispatched.LoadAndDelete(event.Name); ok {
		close(ch.(chan struct{}))
	}
	return sm.processing.wait()
}

// Dispatch sends an event to a specific state machine instance.
// Returns a channel that closes when the event has been fully processed.
//
// Example:
//
//	sm := hsm.Start(...)
//	done := sm.Dispatch(hsm.Event{Name: "start"})
//	<-done // Wait for event processing to complete
func Dispatch[T context.Context](ctx T, event Event) <-chan struct{} {
	// get the hsm from the context
	if hsm, ok := FromContext(ctx); ok {
		// dispatch the event to the hsm
		return hsm.Dispatch(ctx, event)
	}
	return closedChannel
}

// DispatchAll sends an event to all state machine instances in the current context.
// Returns a channel that closes when all instances have processed the event.
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
	return DispatchTo(ctx, event)
}

func DispatchTo(ctx context.Context, event Event, maybeIds ...string) <-chan struct{} {
	instances, ok := ctx.Value(Keys.Instances).(*sync.Map)
	if !ok || instances == nil {
		return closedChannel
	}
	signal := make(chan struct{})
	go func(signal chan struct{}) {
		defer close(signal)
		signals := make(map[string]<-chan struct{})
		instances.Range(func(key, value any) bool {
			snapshot := value.(Instance).takeSnapshot()
			if len(maybeIds) == 0 || Match(snapshot.ID, maybeIds...) {
				signals[key.(string)] = value.(Instance).Dispatch(ctx, event)
			}
			return true
		})
		for len(signals) > 0 {
			for i, ch := range signals {
				select {
				case <-ch:
					delete(signals, i)
				case <-ctx.Done():
					return
				}
			}
		}
	}(signal)
	return signal
}

func Propagate(ctx context.Context, event Event) <-chan struct{} {
	hsm, ok := FromContext(ctx)
	if !ok {
		return closedChannel
	}
	active := hsm.Context()
	owner, ok := FromContext(active.context)
	if !ok {
		return closedChannel
	}
	return owner.Dispatch(ctx, event)
}

func PropagateAll(ctx context.Context, event Event) <-chan struct{} {
	hsm, ok := FromContext(ctx)
	if !ok {
		return closedChannel
	}
	signal := make(chan struct{})
	go func() {
		defer close(signal)
		signals := make(map[any]<-chan struct{})
		active, ok := FromContext(hsm.Context().context)
		for ok {
			signals[active] = active.Dispatch(ctx, event)
			active, ok = FromContext(active.Context().context)
		}
		for len(signals) > 0 {
			for i, ch := range signals {
				select {
				case <-ch:
					delete(signals, i)
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return signal
}

func AfterProcess(ctx context.Context, hsm Instance, maybeEvent ...Event) <-chan struct{} {
	if len(maybeEvent) > 0 {
		ch, _ := hsm.channels().processed.LoadOrStore(maybeEvent[0].Name, make(chan struct{}))
		return ch.(chan struct{})
	} else {
		return hsm.wait()
	}
}

func AfterDispatch(ctx context.Context, hsm Instance, event Event) <-chan struct{} {
	ch, _ := hsm.channels().dispatched.LoadOrStore(event.Name, make(chan struct{}))
	return ch.(chan struct{})
}

func AfterEntry(ctx context.Context, hsm Instance, state string) <-chan struct{} {
	ch, _ := hsm.channels().entered.LoadOrStore(state, make(chan struct{}))
	return ch.(chan struct{})
}

func AfterExit(ctx context.Context, hsm Instance, state string) <-chan struct{} {
	ch, _ := hsm.channels().exited.LoadOrStore(state, make(chan struct{}))
	return ch.(chan struct{})
}

func AfterActivity(ctx context.Context, hsm Instance, state string) <-chan struct{} {
	ch, _ := hsm.channels().activities.LoadOrStore(state, make(chan struct{}))
	return ch.(chan struct{})
}

// FromContext retrieves a state machine instance from a context.
// Returns the instance and a boolean indicating whether it was found.
//
// Example:
//
//	if sm, ok := hsm.FromContext(ctx); ok {
//	    log.Printf("Current state: %s", sm.State())
//	}
func FromContext(ctx context.Context) (Instance, bool) {
	hsm, ok := ctx.Value(Keys.HSM).(Instance)
	if ok {
		return hsm, true
	}
	return nil, false
}

func InstancesFromContext(ctx context.Context) ([]Instance, bool) {
	instancesPointer, ok := ctx.Value(Keys.Instances).(*sync.Map)
	if !ok || instancesPointer == nil {
		return nil, false
	}
	instances := make([]Instance, 0)
	instancesPointer.Range(func(key, value any) bool {
		instances = append(instances, value.(Instance))
		return true
	})
	return instances, true
}

// Stop gracefully stops a state machine instance.
// It cancels any running activities and prevents further event processing.
//
// Example:
//
//	sm := hsm.Start(...)
//	// ... use state machine ...
//	hsm.Stop(sm)
func Stop(ctx context.Context, hsm Instance) <-chan struct{} {
	return hsm.stop(ctx)
}

func Restart(ctx context.Context, hsm Instance, maybeData ...any) <-chan struct{} {
	return hsm.restart(ctx, maybeData...)
}

func ID(hsm Instance) string {
	snapshot := hsm.takeSnapshot()
	return snapshot.ID
}

func QualifiedName(hsm Instance) string {
	snapshot := hsm.takeSnapshot()
	return snapshot.QualifiedName
}

func Name(hsm Instance) string {
	snapshot := hsm.takeSnapshot()
	return path.Base(snapshot.QualifiedName)
}

func TakeSnapshot(ctx context.Context, hsm Instance) Snapshot {
	return hsm.takeSnapshot()
}
