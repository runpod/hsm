package hsm_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/runpod/hsm"
	"github.com/runpod/hsm/pkg/plantuml"
)

type Trace struct {
	sync  []string
	async []string
}

func (t *Trace) reset() {
	t.sync = []string{}
	t.async = []string{}
}

func tracer(ctx context.Context, sm hsm.Instance, step string, data ...any) (context.Context, func(...any)) {
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
		hsm.Final("exit"),
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
		hsm.Transition("wildcard", hsm.Trigger("abcd*"), hsm.Source("/s"), hsm.Target("/s")),
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
		hsm.Transition(hsm.Trigger("X"), hsm.Effect(mockAction("X.transition.effect", false)), hsm.Source("/s/s3"), hsm.Target("/exit")),
	)
	sm := hsm.Start(ctx, &THSM{
		foo: 0,
	}, &model, hsm.Config{
		Trace: tracer,
		Name:  "TestHSM",
		Id:    "test",
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
	if !hsm.Match(sm.State(), "/s/s1/s11") {
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
	time.Sleep(time.Second * 3)
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
	ch := sm.Dispatch(ctx, hsm.Event{
		Name: "J",
		Done: make(chan struct{}),
	})
	<-ch
	<-ch
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
	if sm.State() != "/s/s3" {
		t.Fatal("state is not correct after Z", "state", sm.State())
	}
	if !trace.contains(
		Trace{
			sync: []string{"Z.transition.effect"},
		},
	) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "X",
		Done: make(chan struct{}),
	})
	if sm.State() != "/exit" {
		t.Fatal("state is not correct after X", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s3.exit", "s.exit", "X.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	select {
	case <-sm.Context().Done():
	default:
		t.Fatal("sm is not done after entering top level final state")
	}
	trace.reset()
	<-sm.Stop(ctx)
	if sm.State() != "" {
		t.Fatal("state is not correct", "state", sm.State())
	}

}

func TestMatch(t *testing.T) {
	if !hsm.Match("/synced/exited", "/synced/*") {
		t.Fatal("Match is not correct /foo/bar is a match for /foo/bar/baz")
	}
	if hsm.Match("/foo/bar/baz", "/foo/bar") {
		t.Fatal("Match is not correct /foo/bar is not a match for /foo/bar/baz")
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
	sm2 := hsm.Start(sm1.Context(), &THSM{}, &model)
	if sm2.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	hsm.DispatchAll(sm2.Context(), hsm.Event{
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

func TestEvery(t *testing.T) {
	timestamps := []time.Time{}
	model := hsm.Define(
		"TestHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo"),
		hsm.Transition(
			hsm.Every(func(ctx context.Context, thsm *THSM, event hsm.Event) time.Duration {
				return time.Millisecond * 500
			}),
			hsm.Effect(func(ctx context.Context, thsm *THSM, event hsm.Event) {
				timestamps = append(timestamps, time.Now())
			}),
		),
	)
	_ = hsm.Start(context.Background(), &THSM{}, &model)
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 550)
		if len(timestamps) > i+1 {
			t.Fatalf("timestamps are not in order expected %v got %v", timestamps[i], timestamps[i+1])
		}
	}
	for i := 1; i < len(timestamps)-1; i++ {
		delta := timestamps[i+1].Sub(timestamps[i])
		if delta < time.Millisecond*500 || delta > time.Millisecond*551 {
			t.Fatalf("delta is not correct expected %v got %v", time.Millisecond*500, delta)
		}
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
	sm2 := hsm.Start(sm1.Context(), &THSM{}, &model, hsm.Config{Id: "sm2"})
	if sm1.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm1.State())
	}
	if sm2.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	<-hsm.DispatchTo(sm2.Context(), hsm.Event{
		Name: "foo",
		Done: make(chan struct{}),
	}, "sm*")
	if sm2.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	if sm1.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm1.State())
	}
}

var bytes []byte

func noBehavior(ctx context.Context, hsm *THSM, event hsm.Event) {
	if event.Data != nil {
		copy(bytes, event.Data.([]byte))
	}
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

func TestCompletionEventChannelPassing(t *testing.T) {
	model := hsm.Define(
		"T",
		hsm.Initial(hsm.Target("a")),
		hsm.State("a", hsm.Transition(hsm.Trigger("b"), hsm.Target("../b"))),
		hsm.State("b",
			hsm.Entry(func(ctx context.Context, sm *THSM, event hsm.Event) {
				sm.Dispatch(ctx, hsm.Event{
					Name: "e",
				})
				sm.Dispatch(ctx, hsm.Event{
					Name: "c",
					Kind: hsm.Kinds.CompletionEvent,
				})
			}),
			hsm.Transition(
				hsm.Trigger("c"),
				hsm.Source("."),
				hsm.Target(
					hsm.Choice(
						hsm.Transition(
							hsm.Target("../c"),
							hsm.Guard(func(ctx context.Context, sm *THSM, event hsm.Event) bool {
								return true
							}),
						),
						hsm.Transition(
							hsm.Trigger("d"),
							hsm.Target("../d"),
						),
					),
				),
			),
		),
		hsm.State("c", hsm.Entry(
			func(ctx context.Context, sm *THSM, event hsm.Event) {
				sm.Dispatch(ctx, hsm.Event{
					Name: "e",
				})
				sm.Dispatch(ctx, hsm.Event{
					Name: "d",
					Kind: hsm.Kinds.CompletionEvent,
				})
			},
		),
			hsm.Transition(
				hsm.Trigger("d"),
				hsm.Target("../d"),
			),
		),
		hsm.State("d"),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/a" {
		t.Fatalf("expected state \"/a\" got \"%s\"", sm.State())
	}
	done := sm.Dispatch(context.Background(), hsm.Event{
		Name: "b",
		Done: make(chan struct{}),
	})
	<-done
	if sm.State() != "/d" {
		t.Fatalf("expected state \"/d\" got \"%s\"", sm.State())
	}

}

// var benchSM = hsm.Start(context.Background(), &THSM{}, &benchModel)

func BenchmarkHSM(b *testing.B) {
	ctx := context.Background()
	fooEvent := hsm.Event{
		Name: "foo",
		// Done: make(chan struct{}),
	}
	barEvent := hsm.Event{
		Name: "bar",
		// Done: make(chan struct{}),
	}
	benchSM := hsm.Start(ctx, &THSM{}, &benchModel)
	b.ReportAllocs()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchSM.Dispatch(ctx, fooEvent)
		benchSM.Dispatch(ctx, barEvent)
	}
	slog.Info("done", "queue", benchSM.QueueLen())
	<-benchSM.Stop(ctx)
	slog.Info("done", "queue", benchSM.QueueLen())

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

func BenchmarkHSMWithLargeData(b *testing.B) {
	// Create 1MB of data

	ctx := context.Background()

	instance := hsm.Start(ctx, &THSM{}, &benchModel)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		instance.Dispatch(ctx, hsm.Event{
			Name: "foo",
		})
		instance.Dispatch(ctx, hsm.Event{
			Name: "bar",
		})

	}
	<-instance.Stop(ctx)
}

type TestLogger struct {
	messages []string
}

func NewTestLogger() *TestLogger {
	return &TestLogger{
		messages: []string{},
	}
}

func (l *TestLogger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	// Format message with args
	formattedMsg := msg
	if len(args) > 0 {
		// Simple formatting that's good enough for our test
		for i := 0; i < len(args)-1; i += 2 {
			if i+1 < len(args) {
				formattedMsg += fmt.Sprintf(" %v=%v", args[i], args[i+1])
			}
		}
	}
	l.messages = append(l.messages, formattedMsg)
}

func (l *TestLogger) Contains(substring string) bool {
	for _, msg := range l.messages {
		if strings.Contains(msg, substring) {
			return true
		}
	}
	return false
}

func (l *TestLogger) Reset() {
	l.messages = []string{}
}

func (l *TestLogger) Messages() []string {
	return l.messages
}

func TestLog(t *testing.T) {
	// Create a test logger to capture log output
	logger := NewTestLogger()

	// Create a simple HSM model with logging in entry, exit, and effect actions
	model := hsm.Define(
		"TestLogHSM",
		hsm.State("foo",
			hsm.Entry(hsm.Log[*THSM](slog.LevelInfo, "Entry action for foo state")),
			hsm.Exit(hsm.Log[*THSM](slog.LevelInfo, "Exit action for foo state")),
		),
		hsm.State("bar",
			hsm.Entry(hsm.Log[*THSM](slog.LevelInfo, "Entry action for bar state")),
		),
		hsm.Transition(
			hsm.Trigger("foo"),
			hsm.Source("foo"),
			hsm.Target("bar"),
			hsm.Effect(hsm.Log[*THSM](slog.LevelInfo, "Effect action for foo->bar transition")),
		),
		hsm.Initial(hsm.Target("foo")),
	)

	// Create a context with the logger
	ctx := context.Background()

	// Start the state machine
	sm := hsm.Start(
		ctx,
		&THSM{},
		&model,
		hsm.Config{
			Id:     "test-hsm",
			Name:   "test-hsm",
			Logger: logger,
		},
	)

	// Wait a bit longer for initial state entry
	time.Sleep(100 * time.Millisecond)

	// Check that we have the entry log for foo state
	if !logger.Contains("Entry action for foo state ") {
		t.Fatalf("Expected log output to contain entry action for foo state, got: %v", logger.Messages())
	}

	// Reset the logger to start fresh
	logger.Reset()

	done := sm.Dispatch(ctx, hsm.Event{Name: "foo"})
	<-done // Wait for the event to be processed

	// Add a delay for logs to be processed
	time.Sleep(100 * time.Millisecond)

	// Check log output for exit, effect, and entry actions
	if !logger.Contains("Exit action for foo state") {
		t.Fatalf("Expected log output to contain exit action for foo state, got: %v", logger.Messages())
	}
	if !logger.Contains("Effect action for foo->bar transition") {
		t.Fatalf("Expected log output to contain effect action for transition, got: %v", logger.Messages())
	}
	if !logger.Contains("Entry action for bar state") {
		t.Fatalf("Expected log output to contain entry action for bar state, got: %v", logger.Messages())
	}

	// Clean up
	<-sm.Stop(ctx)
}

func TestRestart(t *testing.T) {
	model := hsm.Define(
		"TestRestartHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
		hsm.Transition(hsm.Trigger("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.State("bar", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
	<-sm.Dispatch(context.Background(), hsm.Event{Name: "foo", Done: make(chan struct{})})
	if sm.State() != "/bar" {
		t.Fatalf("Expected state to be bar, got: %s", sm.State())
	}
	sm.Restart(context.Background())
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
}

func TestDispatch(t *testing.T) {
	model := hsm.Define(
		"TestDispatchHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
		hsm.Transition(hsm.Trigger("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.State("bar", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
	done := hsm.Dispatch(sm.Context(), hsm.Event{Name: "foo", Done: make(chan struct{})})
	<-done
	if sm.State() != "/bar" {
		t.Fatalf("Expected state to be bar, got: %s", sm.State())
	}
	<-sm.Stop(context.Background())
	select {
	case <-sm.Context().Done():
	default:
		t.Fatalf("Expected state machine to be done")
	}

}
