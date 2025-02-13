package hsm_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/stateforward/go-hsm"
	"github.com/stateforward/go-hsm/pkg/plantuml"
)

type Trace struct {
	sync  []string
	async []string
}

func (t *Trace) reset() {
	t.sync = []string{}
	t.async = []string{}
}

func tracer(ctx context.Context, step string, data ...any) (context.Context, func(...any)) {
	return ctx, func(data ...any) {}
}

func (t *Trace) matches(expected Trace) bool {
	if expected.sync != nil && !slices.Equal(t.sync, expected.sync) {
		return false
	}
	if expected.async != nil && !slices.Equal(t.async, expected.async) {
		return false
	}
	return true
}

func (t *Trace) contains(expected Trace) bool {
	if expected.sync != nil && slices.ContainsFunc(t.sync, func(s string) bool {
		return slices.Contains(expected.sync, s)
	}) {
		return true
	}
	if expected.async != nil && slices.ContainsFunc(t.async, func(s string) bool {
		return slices.Contains(expected.async, s)
	}) {
		return true
	}
	return false
}

type Event struct{}

type THSM struct {
	hsm.HSM
	foo int
}

func TestHSM(t *testing.T) {
	trace := &Trace{}
	// test
	mockAction := func(name string, async bool) func(ctx context.Context, thsm *THSM, event hsm.Event) {
		return func(ctx context.Context, thsm *THSM, event hsm.Event) {
			if async {
				trace.async = append(trace.async, name)
			} else {
				trace.sync = append(trace.sync, name)
			}
		}
	}
	afterTriggered := false
	dEvent := hsm.Event{
		Name: "D",
	}
	ctx := context.Background()
	model := hsm.Define(
		"TestHSM",
		hsm.State("s",
			hsm.Entry(mockAction("s.entry", false)),
			hsm.Activity(mockAction("s.activity", true)),
			hsm.Exit(mockAction("s.exit", false)),
			hsm.State("s1",
				hsm.State("s11",
					hsm.Entry(mockAction("s11.entry", false)),
					hsm.Activity(mockAction("s11.activity", true)),
					hsm.Exit(mockAction("s11.exit", false)),
				),
				hsm.Initial(hsm.Target("s11"), hsm.Effect(mockAction("s1.initial.effect", false))),
				hsm.Exit(mockAction("s1.exit", false)),
				hsm.Entry(mockAction("s1.entry", false)),
				hsm.Activity(mockAction("s1.activity", true)),
				hsm.Transition(hsm.Trigger("I"), hsm.Effect(mockAction("s1.I.transition.effect", false))),
				hsm.Transition(hsm.Trigger("A"), hsm.Target("/s/s1"), hsm.Effect(mockAction("s1.A.transition.effect", false))),
				hsm.Transition(hsm.Trigger("0")),
			),
			hsm.Transition(hsm.Trigger("D"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s1"), hsm.Effect(mockAction("s11.D.transition.effect", false)), hsm.Guard(
				func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
					check := hsm.foo == 1
					hsm.foo = 0
					return check
				},
			)),
			hsm.Initial(hsm.Target("s1/s11"), hsm.Effect(mockAction("s.initial.effect", false))),
			hsm.State("s2",
				hsm.Entry(mockAction("s2.entry", false)),
				hsm.Activity(mockAction("s2.activity", true)),
				hsm.Exit(mockAction("s2.exit", false)),
				hsm.State("s21",
					hsm.State("s211",
						hsm.Entry(mockAction("s211.entry", false)),
						hsm.Activity(mockAction("s211.activity", true)),
						hsm.Exit(mockAction("s211.exit", false)),
						hsm.Transition(hsm.Trigger("G"), hsm.Target("/s/s1/s11"), hsm.Effect(mockAction("s211.G.transition.effect", false))),
					),
					hsm.Initial(hsm.Target("s211"), hsm.Effect(mockAction("s21.initial.effect", false))),
					hsm.Entry(mockAction("s21.entry", false)),
					hsm.Activity(mockAction("s21.activity", true)),
					hsm.Exit(mockAction("s21.exit", false)),
					hsm.Transition(hsm.Trigger("A"), hsm.Target("/s/s2/s21")), // self transition
				),
				hsm.Initial(hsm.Target("s21/s211"), hsm.Effect(mockAction("s2.initial.effect", false))),
				hsm.Transition(hsm.Trigger("C"), hsm.Target("/s/s1"), hsm.Effect(mockAction("s2.C.transition.effect", false))),
			),
			hsm.State("s3",
				hsm.Entry(mockAction("s3.entry", false)),
				hsm.Activity(mockAction("s3.activity", true)),
				hsm.Exit(mockAction("s3.exit", false)),
			),
			hsm.Transition(hsm.Trigger(`*.P.*`), hsm.Effect(mockAction("s11.P.transition.effect", false))),
		),
		hsm.State("t"),
		hsm.Initial(
			hsm.Target(hsm.Choice(
				"initial_choice",
				hsm.Transition(hsm.Target("/s/s2")),
			)), hsm.Effect(mockAction("initial.effect", false))),
		hsm.Transition(hsm.Trigger("D"), hsm.Source("/s/s1"), hsm.Target("/s"), hsm.Effect(mockAction("s1.D.transition.effect", false)), hsm.Guard(
			func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
				check := hsm.foo == 0
				hsm.foo++
				return check
			},
		)),
		hsm.Transition(hsm.Trigger(dEvent), hsm.Source("/s"), hsm.Target("/s"), hsm.Effect(mockAction("s.D.transition.effect", false))),
		hsm.Transition(hsm.Trigger("C"), hsm.Source("/s/s1"), hsm.Target("/s/s2"), hsm.Effect(mockAction("s1.C.transition.effect", false))),
		hsm.Transition(hsm.Trigger("E"), hsm.Source("/s"), hsm.Target("/s/s1/s11"), hsm.Effect(mockAction("s.E.transition.effect", false))),
		hsm.Transition(hsm.Trigger("G"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s2/s21/s211"), hsm.Effect(mockAction("s11.G.transition.effect", false))),
		hsm.Transition(hsm.Trigger("I"), hsm.Source("/s"), hsm.Effect(mockAction("s.I.transition.effect", false)), hsm.Guard(
			func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
				check := hsm.foo == 0
				hsm.foo++
				return check
			},
		)),
		hsm.Transition(hsm.After(
			func(ctx context.Context, hsm *THSM, event hsm.Event) time.Duration {
				return time.Second * 2
			},
			"s211.after",
		), hsm.Source("/s/s2/s21/s211"), hsm.Target("/s/s1/s11"), hsm.Effect(mockAction("s211.after.transition.effect", false)), hsm.Guard(
			func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
				triggered := !afterTriggered
				afterTriggered = true
				return triggered
			},
		)),
		hsm.Transition(hsm.Trigger("H"), hsm.Source("/s/s1/s11"), hsm.Target(
			hsm.Choice(
				hsm.Transition(hsm.Target("/s/s1"), hsm.Guard(
					func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
						return hsm.foo == 0
					},
				)),
				hsm.Transition(hsm.Target("/s/s2"), hsm.Effect(mockAction("s11.H.choice.transition.effect", false))),
			),
		), hsm.Effect(mockAction("s11.H.transition.effect", false))),
		hsm.Transition(hsm.Trigger("J"), hsm.Source("/s/s2/s21/s211"), hsm.Target("/s/s1/s11"), hsm.Effect(func(ctx context.Context, thsm *THSM, event hsm.Event) {
			trace.async = append(trace.async, "s11.J.transition.effect")
			thsm.Dispatch(ctx, hsm.Event{
				Name: "K",
				Done: event.Done,
			})
		})),
		hsm.Transition(hsm.Trigger("K"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s3"), hsm.Effect(mockAction("s11.K.transition.effect", false))),
		hsm.Transition(hsm.Trigger("Z"), hsm.Effect(mockAction("Z.transition.effect", false))),
	)
	sm := hsm.Start(ctx, &THSM{
		foo: 0,
	}, &model, hsm.Config{
		Trace: tracer,
	})
	plantuml.Generate(os.Stdout, &model)
	if sm.State() != "/s/s2/s21/s211" {
		t.Fatal("Initial state is not /s/s2/s21/s211", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"initial.effect", "s.entry", "s2.entry", "s2.initial.effect", "s21.entry", "s211.entry"},
	}) {
		t.Fatal("Trace is not correct", "trace", trace)
	}

	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "G",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s211.exit", "s21.exit", "s2.exit", "s211.G.transition.effect", "s1.entry", "s11.entry"},
	}) {
		t.Fatal("trace is not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "I",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s1.I.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "A",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s11.exit", "s1.exit", "s1.A.transition.effect", "s1.entry", "s1.initial.effect", "s11.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "D",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s11.exit", "s1.exit", "s1.D.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "D",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s.exit", "s.D.transition.effect", "s.entry", "s.initial.effect", "s1.entry", "s11.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "D",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s11.exit", "s11.D.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "C",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s2/s21/s211" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s1.exit", "s1.C.transition.effect", "s2.entry", "s2.initial.effect", "s21.entry", "s211.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "E",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s211.exit", "s21.exit", "s2.exit", "s.E.transition.effect", "s1.entry", "s11.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "E",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s11.exit", "s1.exit", "s.E.transition.effect", "s1.entry", "s11.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "G",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s2/s21/s211" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s11.exit", "s1.exit", "s11.G.transition.effect", "s2.entry", "s21.entry", "s211.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "I",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s2/s21/s211" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s.I.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	select {
	case <-sm.Wait("/s/s1/s11"):
	case <-time.After(3 * time.Second):
		t.Fatalf("wait timedout")
	}
	if sm.State() != "/s/s1/s11" {
		t.Fatal("state is not correct after `after` transition", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s211.exit", "s21.exit", "s2.exit", "s211.after.transition.effect", "s1.entry", "s11.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "H",
		Done: make(chan struct{}),
	})
	if sm.State() != "/s/s2/s21/s211" {
		t.Fatal("state is not correct after H", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s11.H.transition.effect", "s11.exit", "s1.exit", "s11.H.choice.transition.effect", "s2.entry", "s2.initial.effect", "s21.entry", "s211.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "J",
		Done: make(chan struct{}),
	})
	<-sm.Wait("/s/s3")
	if sm.State() != "/s/s3" {
		t.Fatal("state is not correct after J expected /s/s3 got", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s211.exit", "s21.exit", "s2.exit", "s1.entry", "s11.entry", "s11.exit", "s1.exit", "s11.K.transition.effect", "s3.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "K.P.A",
		Done: make(chan struct{}),
	})
	if !trace.contains(Trace{
		sync: []string{"s11.P.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{Name: "Z", Done: make(chan struct{})})
	if !trace.contains(
		Trace{
			sync: []string{"Z.transition.effect"},
		},
	) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Stop(ctx)
	if sm.State() != "" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s3.exit", "s.exit"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
}

func TestHSMDispatchAll(t *testing.T) {
	model := hsm.Define(
		"TestHSM",
		hsm.State("foo"),
		hsm.State("bar"),
		hsm.Transition(hsm.Trigger("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.Transition(hsm.Trigger("bar"), hsm.Source("bar"), hsm.Target("foo")),
		hsm.Initial(hsm.Target("foo")),
	)
	ctx := context.Background()
	sm1 := hsm.Start(ctx, &THSM{}, &model)
	sm2 := hsm.Start(sm1, &THSM{}, &model)
	if sm2.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	hsm.DispatchAll(sm2, hsm.Event{
		Name: "foo",
	})
	time.Sleep(time.Second)
	if sm1.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm1.State())
	}
	if sm2.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
}

func TestDispatchTo(t *testing.T) {
	model := hsm.Define(
		"TestHSM",
		hsm.State("foo"),
		hsm.State("bar"),
		hsm.Transition(hsm.Trigger("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.Transition(hsm.Trigger("bar"), hsm.Source("bar"), hsm.Target("foo")),
		hsm.Initial(hsm.Target("foo")),
	)
	ctx := context.Background()
	sm1 := hsm.Start(ctx, &THSM{}, &model, hsm.Config{Id: "sm1"})
	sm2 := hsm.Start(sm1, &THSM{}, &model, hsm.Config{Id: "sm2"})
	if sm2.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	<-hsm.DispatchTo(sm2, "sm2", hsm.Event{
		Name: "foo",
		Done: make(chan struct{}),
	})
	if sm2.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
}
func noBehavior(ctx context.Context, hsm *THSM, event hsm.Event) {
}

func TestIsAncestor(t *testing.T) {
	if !hsm.IsAncestor("/foo/bar", "/foo/bar/baz") {
		t.Fatal("IsAncestor is not correct /foo/bar is an ancestor of /foo/bar/baz")
	}
	if hsm.IsAncestor("/foo/bar/baz", "/foo/bar") {
		t.Fatal("IsAncestor is not correct /foo/bar/baz is not an ancestor of /foo/bar")
	}
	if hsm.IsAncestor("/foo/bar/baz", "/foo/bar/baz") {
		t.Fatal("IsAncestor is not correct /foo/bar/baz is not an ancestor of /foo/bar/baz")
	}
	if !hsm.IsAncestor("/foo/bar/baz", "/foo/bar/baz/qux") {
		t.Fatal("IsAncestor is not correct /foo/bar/baz is an ancestor of /foo/bar/baz/qux")
	}
	if !hsm.IsAncestor("/", "/foo/bar/baz/qux") {
		t.Fatal("IsAncestor is not correct / is an ancestor of /foo/bar/baz/qux")
	}
	if !hsm.IsAncestor("/foo/", "/foo/bar/baz/qux") {
		t.Fatal("IsAncestor is not correct /foo/ is an ancestor of /foo/bar/baz/qux")
	}
}

func TestLCA(t *testing.T) {
	if hsm.LCA("/foo/bar", "/foo/bar/baz") != "/foo/bar" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("/foo/bar", "/foo/bar/baz"))
	}
	if hsm.LCA("/foo/bar/baz", "/foo/bar") != "/foo/bar" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("/foo/bar/baz", "/foo/bar"))
	}
	if hsm.LCA("/foo/bar/baz", "/foo/bar/baz") != "/foo/bar" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("/foo/bar/baz", "/foo/bar/baz"))
	}
	if hsm.LCA("/foo/bar/baz", "/foo/bar/baz/qux") != "/foo/bar/baz" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("/foo/bar/baz", "/foo/bar/baz/qux"))
	}
	if hsm.LCA("/", "/foo/bar/baz/qux") != "/" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("/", "/foo/bar/baz/qux"))
	}
	if hsm.LCA("", "/foo/bar/baz/qux") != "/foo/bar/baz/qux" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("", "/foo/bar/baz/qux"))
	}
	if hsm.LCA("/foo/bar/baz/qux", "") != "/foo/bar/baz/qux" {
		t.Fatal("LCA is not correct", "LCA", hsm.LCA("/foo/bar/baz/qux", ""))
	}
}

// }
var benchModel = hsm.Define(
	"TestHSM",
	hsm.State("foo", hsm.Entry(noBehavior),
		hsm.Exit(noBehavior)),
	hsm.State("bar", hsm.Entry(noBehavior),
		hsm.Exit(noBehavior)),
	hsm.Transition(
		hsm.Trigger("foo"),
		hsm.Source("foo"),
		hsm.Target("bar"),
		hsm.Effect(noBehavior),
	),
	hsm.Transition(
		hsm.Trigger("bar"),
		hsm.Source("bar"),
		hsm.Target("foo"),
		hsm.Effect(noBehavior),
	),
	hsm.Initial(hsm.Target("foo"), hsm.Effect(noBehavior)),
	// hsm.Telemetry(provider.Tracer("github.com/stateforward/go-hsm")),
)
var benchSM = hsm.Start(context.Background(), &THSM{}, &benchModel)

func BenchmarkHSM(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	fooEvent := hsm.Event{
		Name: "foo",
		// Done: make(chan struct{}),
	}
	barEvent := hsm.Event{
		Name: "bar",
		// Done: make(chan struct{}),
	}
	benchSM = hsm.Start(ctx, &THSM{}, &benchModel)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSM.Dispatch(ctx, fooEvent)
		// if benchSM.State() != "/bar" {
		// 	b.Fatal("state is not correct, expected /bar got", "state", benchSM.State())
		// }
		benchSM.Dispatch(ctx, barEvent)
		// if benchSM.State() != "/foo" {
		// 	b.Fatal("state is not correct, expected /foo got", "state", benchSM.State())
		// }
	}
	// <-benchSM.Stop(ctx)
}

func nonHSMLogic() func(event *hsm.Event) bool {
	type state int
	const (
		foo state = iota
		bar
	)
	currentState := foo
	// Simulating entry/exit actions as no-ops to match HSM version
	fooEntry := func(event *hsm.Event) {}
	fooExit := func(event *hsm.Event) {}
	barEntry := func(event *hsm.Event) {}
	barExit := func(event *hsm.Event) {}
	initialEffect := func(event *hsm.Event) {}

	// Transition effects as no-ops
	fooToBarEffect := func(event *hsm.Event) {}
	barToFooEffect := func(event *hsm.Event) {}

	// Event handling
	handleEvent := func(event *hsm.Event) bool {
		switch currentState {
		case foo:
			if event.Name == "foo" {
				fooExit(event)
				fooToBarEffect(event)
				currentState = bar
				barEntry(event)
				return true
			}
		case bar:
			if event.Name == "bar" {
				barExit(event)
				barToFooEffect(event)
				currentState = foo
				fooEntry(event)
				return true
			}
		}
		return false
	}

	// Initial transition
	initialEffect(nil)
	fooEntry(nil)
	return handleEvent
}

func BenchmarkNonHSM(b *testing.B) {
	handler := nonHSMLogic()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !handler(&hsm.Event{
			Name: "foo",
		}) {
			b.Fatal("event not handled")
		}
		if !handler(&hsm.Event{
			Name: "bar",
		}) {
			b.Fatal("event not handled")
		}
	}
}
