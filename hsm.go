package hsm

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stateforward/go-hsm/embedded"
	"github.com/stateforward/go-hsm/kinds"
	"github.com/stateforward/go-hsm/pkg/telemetry"
	"github.com/stateforward/go-hsm/queue"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

/******* Element *******/

type element struct {
	kind          uint64
	qualifiedName string
	id            string
	attributes    []attribute.KeyValue
}

func (element *element) Kind() uint64 {
	if element == nil {
		return 0
	}
	return element.kind
}

func (element *element) Owner() string {
	if element == nil {
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

type Model struct {
	state
	namespace map[string]embedded.Element
	elements  []RedifinableElement
	trace     trace.Tracer
}

func (model *Model) Namespace() map[string]embedded.Element {
	return model.namespace
}

func (model *Model) Push(partial RedifinableElement) {
	model.elements = append(model.elements, partial)
}

type RedifinableElement = func(model *Model, stack []embedded.Element) embedded.Element

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
	entry    string
	exit     string
	activity string
}

func (state *state) Entry() string {
	return state.entry
}

func (state *state) Activity() string {
	return state.activity
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
	events []embedded.Event
	paths  map[string]paths
}

func (transition *transition) Guard() string {
	return transition.guard
}

func (transition *transition) Effect() string {
	return transition.effect
}

func (transition *transition) Events() []embedded.Event {
	return transition.events
}

func (transition *transition) Source() string {
	return transition.source
}

func (transition *transition) Target() string {
	return transition.target
}

/******* Behavior *******/

type behavior[T context.Context] struct {
	element
	action func(ctx Context[T], event Event)
}

/******* Constraint *******/

type constraint[T context.Context] struct {
	element
	expression func(ctx Context[T], event Event) bool
}

/******* Active *******/

type active struct {
	context.Context
	channel chan struct{}
	cancel  context.CancelFunc
}

/******* Events *******/

type Event = embedded.Event

type event struct {
	element
	data any
}

func (event *event) Name() string {
	if event == nil {
		return ""
	}
	return event.qualifiedName
}

func (event *event) Data() any {
	if event == nil {
		return nil
	}
	return event.data
}

func apply(model *Model, stack []embedded.Element, partials ...RedifinableElement) {
	for _, partial := range partials {
		partial(model, stack)
	}
}

func Define(name string, redifinableElements ...RedifinableElement) Model {
	model := Model{
		state: state{
			vertex: vertex{element: element{kind: kinds.State, qualifiedName: "/"}, transitions: []string{}},
		},
		namespace: map[string]embedded.Element{},
		elements:  redifinableElements,
		trace:     telemetry.NewProvider().Tracer("hsm"),
	}

	stack := []embedded.Element{&model}
	for len(model.elements) > 0 {
		elements := model.elements
		model.elements = []RedifinableElement{}
		apply(&model, stack, elements...)
	}
	model.id = name
	return model
}

func find(stack []embedded.Element, maybeKinds ...uint64) embedded.Element {
	for i := len(stack) - 1; i >= 0; i-- {
		if kinds.IsKind(stack[i].Kind(), maybeKinds...) {
			return stack[i]
		}
	}
	return nil
}

func get[T embedded.Element](model *Model, name string) T {
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

func State(name string, partialElements ...RedifinableElement) RedifinableElement {
	return func(graph *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.StateMachine, kinds.State)
		if owner == nil {
			slog.Error("state must be called within a StateMachine or State")
			panic(fmt.Errorf("state must be called within a StateMachine or State"))
		}
		element := &state{
			vertex: vertex{element: element{kind: kinds.State, qualifiedName: path.Join(owner.QualifiedName(), name)}, transitions: []string{}},
		}
		graph.namespace[element.QualifiedName()] = element
		stack = append(stack, element)
		apply(graph, stack, partialElements...)
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

func Transition[T interface{ RedifinableElement | string }](nameOrPartialElement T, partialElements ...RedifinableElement) RedifinableElement {
	name := ""
	switch any(nameOrPartialElement).(type) {
	case string:
		name = any(nameOrPartialElement).(string)
	case RedifinableElement:
		partialElements = append([]RedifinableElement{any(nameOrPartialElement).(RedifinableElement)}, partialElements...)
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Vertex)
		if owner == nil {
			panic(fmt.Errorf("transition must be called within a State or StateMachine"))
		}
		if name == "" {
			name = fmt.Sprintf("transition_%d", len(model.namespace))
		}
		transition := &transition{
			events: []embedded.Event{},
			element: element{
				kind:          kinds.Transition,
				qualifiedName: path.Join(owner.QualifiedName(), name),
			},
			paths: map[string]paths{},
		}
		model.namespace[transition.QualifiedName()] = transition
		stack = append(stack, transition)
		apply(model, stack, partialElements...)
		if transition.source == "" {
			transition.source = owner.QualifiedName()
		}
		sourceElement, ok := model.namespace[transition.source]
		if !ok {
			slog.Error("missing source", "id", transition.source)
			panic(fmt.Errorf("missing source %s", transition.source))
		}
		switch source := sourceElement.(type) {
		case *state:
			source.transitions = append(source.transitions, transition.QualifiedName())
		case *vertex:
			source.transitions = append(source.transitions, transition.QualifiedName())
		}
		if len(transition.events) == 0 && !kinds.IsKind(sourceElement.Kind(), kinds.Pseudostate) {

			// TODO: completion transition
			qualifiedName := path.Join(transition.source, ".completion")
			transition.events = append(transition.events, &event{
				element: element{kind: kinds.CompletionEvent, qualifiedName: qualifiedName},
			})
			panic(fmt.Errorf("completion transition not implemented"))
		}
		var kind uint64
		if transition.target == transition.source {
			kind = kinds.Self
		} else if transition.target == "" {
			kind = kinds.Internal
		} else if IsAncestor(transition.source, transition.target) {
			kind = kinds.Local
		} else {
			kind = kinds.External
		}
		transition.kind = kind
		enter := []string{}
		entering := transition.target
		lca := LCA(transition.source, transition.target)
		for entering != lca && entering != "/" && entering != "" {
			enter = append([]string{entering}, enter...)
			entering = path.Dir(entering)
		}
		if kinds.IsKind(transition.kind, kinds.Self) {
			enter = append(enter, sourceElement.QualifiedName())
		}
		if kinds.IsKind(sourceElement.Kind(), kinds.Initial) {
			transition.paths[path.Dir(sourceElement.QualifiedName())] = paths{
				enter: enter,
				exit:  []string{sourceElement.QualifiedName()},
			}
		} else {
			model.Push(func(model *Model, stack []embedded.Element) embedded.Element {
				// precompute transition paths for the source state and nested states
				for qualifiedName, element := range model.namespace {
					if strings.HasPrefix(qualifiedName, transition.source) && kinds.IsKind(element.Kind(), kinds.Vertex, kinds.StateMachine) {
						exit := []string{}
						if kind != kinds.Internal {
							exiting := element.QualifiedName()
							for exiting != lca && exiting != "/" && exiting != "" {
								exit = append(exit, exiting)
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

func Source[T interface{ RedifinableElement | string }](nameOrPartialElement T) RedifinableElement {
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("source must be called within a Transition"))
		}
		var name string
		switch any(nameOrPartialElement).(type) {
		case string:
			name = any(nameOrPartialElement).(string)
			if !path.IsAbs(name) {
				if ancestor := find(stack, kinds.State); ancestor != nil {
					name = path.Join(ancestor.QualifiedName(), name)
				}
			}
			// push a validation step to ensure the source exists after the model is built
			model.Push(func(model *Model, stack []embedded.Element) embedded.Element {
				if _, ok := model.namespace[name]; !ok {
					slog.Error("missing source", "id", name)
					panic(fmt.Errorf("missing source %s", name))
				}
				return owner
			})
		case RedifinableElement:
			element := any(nameOrPartialElement).(RedifinableElement)(model, stack)
			if element == nil {
				panic(fmt.Errorf("source is nil"))
			}
			name = element.QualifiedName()
		}
		owner.(*transition).source = name
		return owner
	}
}

func Defer(events ...uint64) RedifinableElement {
	panic("not implemented")
}

func Target[T interface{ RedifinableElement | string }](nameOrPartialElement T) RedifinableElement {
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("Target() must be called within a Transition"))
		}
		transition := owner.(*transition)
		if transition.target != "" {
			panic(fmt.Errorf("transition %s already has target %s", transition.QualifiedName(), transition.target))
		}
		var qualifiedName string
		switch target := any(nameOrPartialElement).(type) {
		case string:
			qualifiedName = target
			if !path.IsAbs(qualifiedName) {
				if ancestor := find(stack, kinds.State); ancestor != nil {
					qualifiedName = path.Join(ancestor.QualifiedName(), qualifiedName)
				}
			}
			// push a validation step to ensure the target exists after the model is built
			model.Push(func(model *Model, stack []embedded.Element) embedded.Element {
				if _, exists := model.namespace[qualifiedName]; !exists {
					panic(fmt.Errorf("missing target %s for transition %s", target, transition.QualifiedName()))
				}
				return transition
			})
		case RedifinableElement:
			targetElement := target(model, stack)
			if targetElement == nil {
				panic(fmt.Errorf("target is nil"))
			}
			qualifiedName = targetElement.QualifiedName()
		}

		transition.target = qualifiedName
		return transition
	}
}

func Effect[T context.Context](fn func(hsm Context[T], event Event), maybeName ...string) RedifinableElement {
	name := ".effect"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			slog.Error("effect must be called within a Transition")
			panic(fmt.Errorf("effect must be called within a Transition"))
		}
		behavior := &behavior[T]{
			element: element{kind: kinds.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
			action:  fn,
		}
		model.namespace[behavior.QualifiedName()] = behavior
		owner.(*transition).effect = behavior.QualifiedName()
		return owner
	}
}

func Guard[T context.Context](fn func(hsm Context[T], event Event) bool, maybeName ...string) RedifinableElement {
	name := ".guard"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("guard must be called within a Transition"))
		}
		constraint := &constraint[T]{
			element:    element{kind: kinds.Constraint, qualifiedName: path.Join(owner.QualifiedName(), name)},
			expression: fn,
		}
		model.namespace[constraint.QualifiedName()] = constraint
		owner.(*transition).guard = constraint.QualifiedName()
		return owner
	}
}

func Initial[T interface{ string | RedifinableElement }](elementOrName T, partialElements ...RedifinableElement) RedifinableElement {
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.StateMachine, kinds.State)
		if owner == nil {
			panic(fmt.Errorf("initial must be called within a Model or State"))
		}
		initial := &vertex{
			element: element{kind: kinds.Initial, qualifiedName: path.Join(owner.QualifiedName(), ".initial")},
		}
		if model.namespace[initial.QualifiedName()] != nil {
			panic(fmt.Errorf("initial state already exists"))
		}
		var target string
		switch any(elementOrName).(type) {
		case string:
			if !path.IsAbs(any(elementOrName).(string)) {
				target = path.Join(owner.QualifiedName(), any(elementOrName).(string))
			} else {
				target = any(elementOrName).(string)
			}
		case RedifinableElement:
			maybeTarget := any(elementOrName).(RedifinableElement)(model, stack)
			if maybeTarget != nil {
				target = maybeTarget.QualifiedName()
			}
		}
		model.namespace[initial.QualifiedName()] = initial
		stack = append(stack, initial)
		transition := (Transition(Target(target), append(partialElements, Source(initial.QualifiedName()))...)(model, stack)).(*transition)
		// validation logic
		if transition.guard != "" {
			panic(fmt.Errorf("initial %s cannot have a guard", initial.QualifiedName()))
		}
		if len(transition.events) > 0 {
			panic(fmt.Errorf("initial %s cannot have triggers", initial.QualifiedName()))
		}
		if !strings.HasPrefix(transition.target, owner.QualifiedName()) {
			panic(fmt.Errorf("initial %s must target a nested state not %s", initial.QualifiedName(), transition.target))
		}
		if len(initial.transitions) > 1 {
			panic(fmt.Errorf("initial %s cannot have multiple transitions %v", initial.QualifiedName(), initial.transitions))
		}
		return transition
	}
}

func Choice[T interface{ RedifinableElement | string }](elementOrName T, partialElements ...RedifinableElement) RedifinableElement {
	name := ""
	switch any(elementOrName).(type) {
	case string:
		name = any(elementOrName).(string)
	case RedifinableElement:
		partialElements = append([]RedifinableElement{any(elementOrName).(RedifinableElement)}, partialElements...)
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.State, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("choice must be called within a State or Transition"))
		} else if kinds.IsKind(owner.Kind(), kinds.Transition) {
			source := owner.(*transition).source
			owner = model.namespace[source]
			if owner == nil {
				panic(fmt.Errorf("missing source %s", source))
			}
		}
		if name == "" {
			name = fmt.Sprintf("choice_%d", len(model.elements))
		}
		qualifiedName := path.Join(owner.QualifiedName(), name)
		element := &vertex{
			element: element{kind: kinds.Choice, qualifiedName: qualifiedName},
		}
		model.namespace[qualifiedName] = element
		stack = append(stack, element)
		apply(model, stack, partialElements...)
		if len(element.transitions) == 0 {
			slog.Error("choice must have at least one transition")
			panic(fmt.Errorf("choice must have at least one transition"))
		}
		if defaultTransition := get[embedded.Transition](model, element.transitions[len(element.transitions)-1]); defaultTransition != nil {
			if defaultTransition.Guard() != "" {
				panic(fmt.Errorf("the last transition of a choice state cannot have a guard"))
			}
		}
		return element
	}
}

func Entry[T context.Context](fn func(ctx Context[T], event Event), maybeName ...string) RedifinableElement {
	name := ".entry"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.State)
		if owner == nil {
			slog.Error("entry must be called within a State")
			panic(fmt.Errorf("entry must be called within a State"))
		}
		element := &behavior[T]{
			element: element{kind: kinds.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
			action:  fn,
		}
		model.namespace[element.QualifiedName()] = element
		owner.(*state).entry = element.QualifiedName()
		return element
	}
}

func Activity[T context.Context](fn func(ctx Context[T], event Event), maybeName ...string) RedifinableElement {
	name := ".activity"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.State)
		if owner == nil {
			slog.Error("activity must be called within a State")
			panic(fmt.Errorf("activity must be called within a State"))
		}

		element := &behavior[T]{
			element: element{kind: kinds.Concurrent, qualifiedName: path.Join(owner.QualifiedName(), name)},
			action:  fn,
		}
		model.namespace[element.QualifiedName()] = element
		owner.(*state).activity = element.QualifiedName()
		return element
	}
}

func Telemetry[T any](providerOrTracer T) RedifinableElement {
	var tracer trace.Tracer
	switch unknown := any(providerOrTracer).(type) {
	case trace.TracerProvider:
		tracer = unknown.Tracer("hsm")
	case trace.Tracer:
		tracer = unknown
	default:
		panic(fmt.Errorf("invalid telemetry provider or tracer"))
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		model.trace = tracer
		return nil
	}
}

func Exit[T context.Context](fn func(ctx Context[T], event Event), maybeName ...string) RedifinableElement {
	name := ".exit"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.State)
		if owner == nil {
			slog.Error("exit must be called within a State")
			panic(fmt.Errorf("exit must be called within a State"))
		}

		element := &behavior[T]{
			element: element{kind: kinds.Behavior, qualifiedName: path.Join(owner.QualifiedName(), name)},
			action:  fn,
		}
		model.namespace[element.QualifiedName()] = element
		owner.(*state).exit = element.QualifiedName()
		return element
	}
}

func Trigger[T interface{ string | *event }](events ...T) RedifinableElement {
	return func(model *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("trigger must be called within a Transition"))
		}
		transition := owner.(*transition)
		for _, eventOrName := range events {
			switch any(eventOrName).(type) {
			case string:
				name := any(eventOrName).(string)
				transition.events = append(transition.events, &event{
					element: element{kind: kinds.Event, qualifiedName: name},
				})
			case *event:
				event := any(eventOrName).(*event)
				transition.events = append(transition.events, event)
			}
		}
		return owner
	}
}

func After[T context.Context](expr func(hsm Context[T]) time.Duration, maybeName ...string) RedifinableElement {
	name := ".after"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(builder *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("after must be called within a Transition"))
		}
		qualifiedName := path.Join(owner.QualifiedName(), strconv.Itoa(len(owner.(*transition).events)), name)
		owner.(*transition).events = append(owner.(*transition).events, &event{
			element: element{kind: kinds.TimeEvent, qualifiedName: qualifiedName},
			data:    expr,
		})
		return owner
	}
}

func Interval[T context.Context](expr func(ctx Context[T], event Event), maybeName ...string) RedifinableElement {
	name := ".interval"
	if len(maybeName) > 0 {
		name = maybeName[0]
	}
	return func(builder *Model, stack []embedded.Element) embedded.Element {
		owner := find(stack, kinds.Transition)
		if owner == nil {
			panic(fmt.Errorf("after must be called within a Transition"))
		}
		qualifiedName := path.Join(owner.QualifiedName(), strconv.Itoa(len(owner.(*transition).events)), name)
		owner.(*transition).events = append(owner.(*transition).events, &event{
			element: element{kind: kinds.TimeEvent, qualifiedName: qualifiedName},
			data:    expr,
		})
		return owner
	}
}

var pool = sync.Pool{
	New: func() any {
		return &event{}
	},
}

func NewEvent(name string, maybeData ...any) *event {
	var data any
	if len(maybeData) > 0 {
		data = maybeData[0]
	}
	event := pool.Get().(*event)
	// id := ""
	// uid, err := uuid.NewV7()
	// if err != nil {
	// 	id = uid.String()
	// }
	event.element = element{kind: kinds.Event, qualifiedName: name}
	event.data = data
	return event
}

func Final(name string) RedifinableElement {
	return func(builder *Model, stack []embedded.Element) embedded.Element {
		panic("not implemented")
	}
}

type Execution = uint32

type HSM[T context.Context] struct {
	context.Context
	behavior[T]
	state      embedded.Element
	model      *Model
	active     map[string]*Context[T]
	queue      *queue.Queue
	processing atomic.Bool
	trace      trace.Tracer
	Storage    T
}

type Context[T context.Context] struct {
	context.Context
	*HSM[T]
	cancel  context.CancelFunc
	channel chan struct{}
}

func (ctx Context[T]) Dispatch(event Event) {
	if ctx.cancel != nil {
		go ctx.HSM.Dispatch(event)
		return
	}
	ctx.HSM.Dispatch(event)
}

type key struct{}

var allKey = key{}

func New[T context.Context](ctx T, model *Model) *HSM[T] {
	hsm := &HSM[T]{
		behavior: behavior[T]{
			element: element{
				kind:          kinds.StateMachine,
				qualifiedName: model.QualifiedName(),
			},
		},
		model:   model,
		active:  map[string]*Context[T]{},
		Storage: ctx,
		trace:   model.trace,
		queue:   queue.New(),
	}
	all, ok := ctx.Value(allKey).(*sync.Map)
	if !ok {
		all = &sync.Map{}
	}
	all.Store(hsm, struct{}{})
	hsm.Context = context.WithValue(ctx, allKey, all)
	hsm.action = func(ctx Context[T], event Event) {
		hsm.processing.Store(true)
		defer hsm.processing.Store(false)
		hsm.state = hsm.initial(&model.state, event)
	}
	hsm.execute(&hsm.behavior, nil)
	return hsm
}

func (hsm *HSM[T]) State() string {
	if hsm == nil {
		return ""
	}
	if hsm.state == nil {
		return ""
	}
	return hsm.state.QualifiedName()
}

func (hsm *HSM[T]) Terminate() {
	if hsm == nil {
		return
	}
	var ok bool
	for hsm.state != nil {
		hsm.exit(hsm.state, nil)
		hsm.state, ok = hsm.model.namespace[hsm.state.Owner()]
		if !ok {
			break
		}
	}
}

func (hsm *HSM[T]) activate(element embedded.Element) *Context[T] {
	current, ok := hsm.active[element.QualifiedName()]
	if !ok {
		current = &Context[T]{
			channel: make(chan struct{}),
		}
		hsm.active[element.QualifiedName()] = current
	}
	current.Context, current.cancel = context.WithCancel(hsm)
	return current
}

func (hsm *HSM[T]) enter(element embedded.Element, event Event, defaultEntry bool) embedded.Element {
	if hsm == nil {
		return nil
	}
	_, span := hsm.trace.Start(hsm, "enter")
	defer span.End()
	switch element.Kind() {
	case kinds.State:
		state := element.(*state)
		if entry := get[*behavior[T]](hsm.model, state.entry); entry != nil {
			hsm.execute(entry, event)
		}
		activity := get[*behavior[T]](hsm.model, state.activity)
		if activity != nil {
			hsm.execute(activity, event)
		}
		for _, qualifiedName := range state.transitions {
			if element := get[*transition](hsm.model, qualifiedName); element != nil {
				for _, event := range element.Events() {
					switch event.Kind() {
					case kinds.TimeEvent:
						ctx := hsm.activate(element)
						go func(ctx *Context[T], event embedded.Event) {
							duration := event.Data().(func(hsm Context[T]) time.Duration)(
								Context[T]{
									Context: ctx,
									HSM:     hsm,
								},
							)
							timer := time.NewTimer(duration)
							defer timer.Stop()
							select {
							case <-ctx.Done():
								ctx.channel <- struct{}{}
								break
							case <-hsm.Done():
								break
							case <-timer.C:
								hsm.Dispatch(event)
								break
							}
							ctx.channel <- struct{}{}
						}(ctx, event)
					}
				}
			}
		}
		if !defaultEntry {
			return element
		}
		return hsm.initial(element, event)
	case kinds.Choice:
		for _, qualifiedName := range element.(*vertex).transitions {
			if transition := get[*transition](hsm.model, qualifiedName); transition != nil {
				if constraint := get[*constraint[T]](hsm.model, transition.Guard()); constraint != nil {
					if !hsm.evaluate(constraint, event) {
						continue
					}
				}
				return hsm.transition(element, transition, event)
			}
		}
	}
	return nil
}

func (hsm *HSM[T]) initial(element embedded.Element, event Event) embedded.Element {
	if hsm == nil || element == nil {
		return nil
	}
	_, span := hsm.trace.Start(hsm, "initial")
	defer span.End()
	var qualifiedName string
	if element.QualifiedName() == "/" {
		qualifiedName = "/.initial"
	} else {
		qualifiedName = element.QualifiedName() + "/.initial"
	}
	if initial := get[*vertex](hsm.model, qualifiedName); initial != nil {
		if len(initial.transitions) > 0 {
			if transition := get[*transition](hsm.model, initial.transitions[0]); transition != nil {
				return hsm.transition(element, transition, event)
			}
		}
	}
	return element
}

func (hsm *HSM[T]) exit(element embedded.Element, event Event) {
	if hsm == nil || element == nil {
		return
	}
	_, span := hsm.trace.Start(hsm, "exit")
	defer span.End()
	if state, ok := element.(*state); ok {
		for _, qualifiedName := range state.transitions {
			if element := get[*transition](hsm.model, qualifiedName); element != nil {
				for _, event := range element.Events() {
					switch event.Kind() {
					case kinds.TimeEvent:
						active, ok := hsm.active[event.QualifiedName()]
						if ok {
							active.cancel()
							<-active.channel
						}
					}
				}
			}
		}
		if activity := get[*behavior[T]](hsm.model, state.activity); activity != nil {
			hsm.terminate(activity)
		}
		if exit := get[*behavior[T]](hsm.model, state.exit); exit != nil {
			hsm.execute(exit, event)
		}
	}

}

func (hsm *HSM[T]) execute(element *behavior[T], event Event) {
	if hsm == nil || element == nil {
		return
	}
	_, span := hsm.trace.Start(hsm, "execute")
	defer span.End()
	switch element.Kind() {
	case kinds.Concurrent:
		ctx := hsm.activate(element)
		go func(ctx *Context[T]) {
			_, span := hsm.trace.Start(hsm, "action")
			defer span.End()
			element.action(Context[T]{
				Context: ctx,
				HSM:     hsm,
			}, event)
			ctx.channel <- struct{}{}
		}(ctx)
	default:
		_, span := hsm.trace.Start(hsm, "action")
		defer span.End()
		element.action(Context[T]{
			Context: hsm.Context,
			HSM:     hsm,
		}, event)

	}

}

func (hsm *HSM[T]) evaluate(guard *constraint[T], event Event) bool {
	if hsm == nil || guard == nil || guard.expression == nil {
		return true
	}
	_, span := hsm.trace.Start(hsm, "evaluate")
	defer span.End()
	return guard.expression(
		Context[T]{
			Context: hsm.Context,
			HSM:     hsm,
		},
		event,
	)
}

func (hsm *HSM[T]) transition(current embedded.Element, transition *transition, event Event) embedded.Element {
	if hsm == nil {
		return nil
	}
	_, span := hsm.trace.Start(hsm, "transition")
	defer span.End()
	path, ok := transition.paths[current.QualifiedName()]
	if !ok {
		return nil
	}
	for _, exiting := range path.exit {
		current, ok = hsm.model.namespace[exiting]
		if !ok {
			return nil
		}
		hsm.exit(current, event)
	}
	if effect := get[*behavior[T]](hsm.model, transition.effect); effect != nil {
		hsm.execute(effect, event)
	}
	if kinds.IsKind(transition.kind, kinds.Internal) {
		return current
	}
	for _, entering := range path.enter {
		next, ok := hsm.model.namespace[entering]
		if !ok {
			return nil
		}
		defaultEntry := entering == transition.target
		current = hsm.enter(next, event, defaultEntry)
		if defaultEntry {
			return current
		}
	}
	current, ok = hsm.model.namespace[transition.target]
	if !ok {
		return nil
	}
	return current
}

func (hsm *HSM[T]) terminate(behavior *behavior[T]) {
	if hsm == nil || behavior == nil {
		return
	}
	_, span := hsm.trace.Start(hsm, "terminate")
	defer span.End()
	active, ok := hsm.active[behavior.QualifiedName()]
	if !ok {
		return
	}
	active.cancel()
	<-active.channel

}

func (hsm *HSM[T]) enabled(source embedded.Vertex, event Event) *transition {
	if hsm == nil {
		return nil
	}
	for _, transitionQualifiedName := range source.Transitions() {
		transition := get[*transition](hsm.model, transitionQualifiedName)
		if transition == nil {
			continue
		}
		for _, evt := range transition.Events() {
			if matched, err := path.Match(evt.Name(), event.Name()); err != nil || !matched {
				continue
			}
			if guard := get[*constraint[T]](hsm.model, transition.Guard()); guard != nil {
				if !hsm.evaluate(guard, event) {
					continue
				}
			}
			return transition
		}
	}
	return nil
}

func (hsm *HSM[T]) process(event embedded.Event) {
	if hsm.processing.Load() {
		return
	}
	hsm.processing.Store(true)
	defer hsm.processing.Store(false)
	for event != nil {
		state := hsm.state.QualifiedName()
		for state != "/" {
			source := get[embedded.Vertex](hsm.model, state)
			if source == nil {
				break
			}
			if transition := hsm.enabled(source, event); transition != nil {
				hsm.state = hsm.transition(hsm.state, transition, event)
				break
			}
			state = source.Owner()
		}
		pool.Put(event)
		event = hsm.queue.Pop()
	}
}

func (hsm *HSM[T]) Dispatch(event Event) {
	if hsm == nil {
		return
	}
	_, span := hsm.trace.Start(hsm, "Dispatch")
	defer span.End()
	if hsm.state == nil {
		return
	}
	if hsm.processing.Load() {
		hsm.queue.Push(event)
		return
	}
	hsm.process(event)
}

func (hsm *HSM[T]) DispatchAll(event Event) {
	active, ok := hsm.Value(allKey).(*sync.Map)
	if !ok {
		return
	}
	_, span := hsm.trace.Start(hsm, "DispatchAll")
	go func(active *sync.Map, span trace.Span) {
		defer span.End()
		active.Range(func(value any, _ any) bool {
			sm, ok := value.(embedded.Context)
			if !ok {
				return true
			}
			sm.Dispatch(event)
			return true
		})
	}(active, span)

}
