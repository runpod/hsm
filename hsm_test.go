package hsm_test

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runpod/hsm/v2"
	"github.com/runpod/hsm/v2/pkg/plantuml"
)

type Trace struct {
	sync  []string
	async []string
	mutex *sync.Mutex
}

func (t *Trace) reset() {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.sync = []string{}
	t.async = []string{}
}

func (t *Trace) matches(expected Trace) bool {
	t.mutex.Lock()
	defer t.mutex.Unlock()
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

func TestComplex(t *testing.T) {
	trace := &Trace{
		mutex: &sync.Mutex{},
	}
	// test
	mockAction := func(name string, async bool) func(ctx context.Context, thsm *THSM, event hsm.Event) {
		return func(ctx context.Context, thsm *THSM, event hsm.Event) {
			trace.mutex.Lock()
			defer trace.mutex.Unlock()
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
				hsm.Transition(hsm.On("I"), hsm.Effect(mockAction("s1.I.transition.effect", false))),
				hsm.Transition(hsm.On("A"), hsm.Target("/s/s1"), hsm.Effect(mockAction("s1.A.transition.effect", false))),
			),
			hsm.Transition(hsm.On("D"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s1"), hsm.Effect(mockAction("s11.D.transition.effect", false)), hsm.Guard(
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
						hsm.Transition(hsm.On("G"), hsm.Target("/s/s1/s11"), hsm.Effect(mockAction("s211.G.transition.effect", false))),
					),
					hsm.Initial(hsm.Target("s211"), hsm.Effect(mockAction("s21.initial.effect", false))),
					hsm.Entry(mockAction("s21.entry", false)),
					hsm.Activity(mockAction("s21.activity", true)),
					hsm.Exit(mockAction("s21.exit", false)),
					hsm.Transition(hsm.On("A"), hsm.Target("/s/s2/s21")), // self transition
				),
				hsm.Initial(hsm.Target("s21/s211"), hsm.Effect(mockAction("s2.initial.effect", false))),
				hsm.Transition(hsm.On("C"), hsm.Target("/s/s1"), hsm.Effect(mockAction("s2.C.transition.effect", false))),
			),
			hsm.State("s3",
				hsm.Entry(mockAction("s3.entry", false)),
				hsm.Activity(mockAction("s3.activity", true)),
				hsm.Exit(mockAction("s3.exit", false)),
			),
			hsm.Transition(hsm.On(`*.P.*`), hsm.Effect(mockAction("s11.P.transition.effect", false))),
		),
		hsm.State("t",
			hsm.Entry(mockAction("t.entry", false)),
			hsm.Activity(mockAction("t.activity", true)),
			hsm.Exit(mockAction("t.exit", false)),
			hsm.State(
				"u",
				hsm.Entry(mockAction("u.entry", false)),
				hsm.Activity(mockAction("u.activity", true)),
				hsm.Exit(mockAction("u.exit", false)),
				hsm.Transition(
					hsm.On("u.t"),
					hsm.Target("/t"),
					hsm.Effect(mockAction("u.t.transition.effect", false)),
				),
			),
			hsm.Transition(
				hsm.On("X"),
				hsm.Target("/exit"),
				hsm.Effect(mockAction("u.X.transition.effect", false)),
			),
		),

		hsm.Final("exit"),
		hsm.Initial(
			hsm.Target(hsm.Choice(
				"initial_choice",
				hsm.Transition(hsm.Target("/s/s2")),
			)), hsm.Effect(mockAction("initial.effect", false))),
		hsm.Transition(hsm.On("D"), hsm.Source("/s/s1"), hsm.Target("/s"), hsm.Effect(mockAction("s1.D.transition.effect", false)), hsm.Guard(
			func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
				check := hsm.foo == 0
				hsm.foo++
				return check
			},
		)),
		hsm.Transition("wildcard", hsm.On("abcd*"), hsm.Source("/s"), hsm.Target("/s")),
		hsm.Transition(hsm.On(dEvent), hsm.Source("/s"), hsm.Target("/s"), hsm.Effect(mockAction("s.D.transition.effect", false))),
		hsm.Transition(hsm.On("C"), hsm.Source("/s/s1"), hsm.Target("/s/s2"), hsm.Effect(mockAction("s1.C.transition.effect", false))),
		hsm.Transition(hsm.On("E"), hsm.Source("/s"), hsm.Target("/s/s1/s11"), hsm.Effect(mockAction("s.E.transition.effect", false))),
		hsm.Transition(hsm.On("G"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s2/s21/s211"), hsm.Effect(mockAction("s11.G.transition.effect", false))),
		hsm.Transition(hsm.On("I"), hsm.Source("/s"), hsm.Effect(mockAction("s.I.transition.effect", false)), hsm.Guard(
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
		hsm.Transition(hsm.On("H"), hsm.Source("/s/s1/s11"), hsm.Target(
			hsm.Choice(
				hsm.Transition(hsm.Target("/s/s1"), hsm.Guard(
					func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
						return hsm.foo == 0
					},
				)),
				hsm.Transition(hsm.Target("/s/s2"), hsm.Effect(mockAction("s11.H.choice.transition.effect", false))),
			),
		), hsm.Effect(mockAction("s11.H.transition.effect", false))),
		hsm.Transition(hsm.On("J"), hsm.Source("/s/s2/s21/s211"), hsm.Target("/s/s1/s11"), hsm.Effect(func(ctx context.Context, thsm *THSM, event hsm.Event) {
			trace.async = append(trace.async, "s11.J.transition.effect")
			thsm.Dispatch(ctx, hsm.Event{
				Name: "K",
			})
		})),
		hsm.Transition(hsm.On("K"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s3"), hsm.Effect(mockAction("s11.K.transition.effect", false))),
		hsm.Transition(hsm.On("Z"), hsm.Effect(mockAction("Z.transition.effect", false))),
		hsm.Transition(hsm.On("X"), hsm.Effect(mockAction("X.transition.effect", false)), hsm.Source("/s/s3"), hsm.Target("/t/u")),
	)
	sm := hsm.Start(ctx, &THSM{
		foo: 0,
	}, &model, hsm.Config{
		Name: "TestHSM",
		ID:   "test",
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
	})
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
	})
	if !trace.contains(Trace{
		sync: []string{"s11.P.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{Name: "Z"})
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
	})
	if sm.State() != "/t/u" {
		t.Fatal("state is not correct after X", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"s3.exit", "s.exit", "X.transition.effect", "t.entry", "u.entry"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "u.t",
	})
	if sm.State() != "/t" {
		t.Fatal("state is not correct after u.t", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"u.exit", "u.t.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	trace.reset()
	<-sm.Dispatch(ctx, hsm.Event{
		Name: "X",
	})
	if sm.State() != "/exit" {
		t.Fatal("state is not correct after X", "state", sm.State())
	}
	if !trace.matches(Trace{
		sync: []string{"t.exit", "u.X.transition.effect"},
	}) {
		t.Fatal("transition actions are not correct", "trace", trace)
	}
	select {
	case <-sm.Context().Done():
	default:
		t.Fatal("sm is not done after entering top level final state")
	}
	trace.reset()
	<-hsm.Stop(ctx, sm)
	if sm.State() != "/" {
		t.Fatal("state is not correct", "state", sm.State())
	}

}

func TestHSMDispatchAll(t *testing.T) {
	model := hsm.Define(
		"TestHSM",
		hsm.State("foo"),
		hsm.State("bar"),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.Transition(hsm.On("bar"), hsm.Source("bar"), hsm.Target("foo")),
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
	mutex := sync.Mutex{}
	model := hsm.Define(
		"TestHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo"),
		hsm.Transition(
			hsm.Every(func(ctx context.Context, thsm *THSM, event hsm.Event) time.Duration {
				return time.Millisecond * 500
			}),
			hsm.Effect(func(ctx context.Context, thsm *THSM, event hsm.Event) {
				mutex.Lock()
				defer mutex.Unlock()
				timestamps = append(timestamps, time.Now())
			}),
		),
	)
	_ = hsm.Start(context.Background(), &THSM{}, &model)
	for i := 0; i < 10; i++ {
		time.Sleep(time.Millisecond * 550)
		mutex.Lock()
		if len(timestamps) > i+1 {
			t.Fatalf("timestamps are not in order expected %v got %v", timestamps[i], timestamps[i+1])
		}
		mutex.Unlock()
	}
	mutex.Lock()
	defer mutex.Unlock()
	for i := 1; i < len(timestamps)-1; i++ {
		delta := timestamps[i+1].Sub(timestamps[i])
		if delta < time.Millisecond*500 || delta > time.Millisecond*551 {
			t.Fatalf("delta is not correct expected %v got %v", time.Millisecond*500, delta)
		}
	}
}

func TestNeverAfter(t *testing.T) {
	effect := atomic.Bool{}
	model := hsm.Define(
		"TestHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo",
			hsm.Transition(
				hsm.After(func(ctx context.Context, sm *THSM, event hsm.Event) time.Duration {
					return time.Second * -1
				}),
				hsm.Effect(func(ctx context.Context, sm *THSM, event hsm.Event) {
					effect.Store(true)
				}),
			),
		),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	time.Sleep(time.Second * 2)
	if effect.Load() {
		t.Fatal("effect should not be called")
	}
}

func TestDispatchTo(t *testing.T) {
	model := hsm.Define(
		"TestHSM",
		hsm.State("foo"),
		hsm.State("bar"),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.Transition(hsm.On("bar"), hsm.Source("bar"), hsm.Target("foo")),
		hsm.Initial(hsm.Target("foo")),
	)
	ctx := context.Background()
	sm1 := hsm.Start(ctx, &THSM{}, &model, hsm.Config{ID: "sm1"})
	sm2 := hsm.Start(sm1.Context(), &THSM{}, &model, hsm.Config{ID: "sm2"})
	if sm1.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm1.State())
	}
	if sm2.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	<-hsm.DispatchTo(sm2.Context(), hsm.Event{
		Name: "foo",
	}, "sm*")
	if sm2.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm2.State())
	}
	if sm1.State() != "/bar" {
		t.Fatal("state is not correct", "state", sm1.State())
	}
}

func noBehavior(ctx context.Context, hsm *THSM, event hsm.Event) {
	// slog.Info("noBehavior", "event", event.Name)
}

func TestChoiceBackToSource(t *testing.T) {
	actions := atomic.Value{}
	actions.Store([]string{})

	makeBehavior := func(name string) func(ctx context.Context, hsm *THSM, event hsm.Event) {
		return func(ctx context.Context, hsm *THSM, event hsm.Event) {
			slog.Info("behavior", "name", name)
			actions.Store(append(actions.Load().([]string), name))
		}
	}
	model := hsm.Define(
		"TestHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(makeBehavior("foo.entry")), hsm.Exit(makeBehavior("foo.exit")), hsm.Choice(
			"choice",
			hsm.Transition(
				hsm.Target("../bar"),
				hsm.Guard(func(ctx context.Context, hsm *THSM, event hsm.Event) bool {
					return false
				}),
			),
			hsm.Transition(hsm.Target("../foo"), hsm.Effect(makeBehavior("foo.choice.effect"))),
		)),
		hsm.State("bar", hsm.Entry(makeBehavior("bar.entry")), hsm.Exit(makeBehavior("bar.exit"))),
		hsm.Transition(hsm.On("choice"), hsm.Source("foo"), hsm.Target("foo/choice")),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	actions.Store([]string{})
	<-sm.Dispatch(context.Background(), hsm.Event{
		Name: "choice",
	})
	if sm.State() != "/foo" {
		t.Fatal("state is not correct", "state", sm.State())
	}
	slog.Info("actions", "actions", actions.Load())

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

func TestInitialEventData(t *testing.T) {
	type data struct {
		foo string
	}
	var configData atomic.Pointer[data]
	model := hsm.Define(
		"TestHSM",
		hsm.Initial(hsm.Target("foo"), hsm.Effect(func(ctx context.Context, sm *THSM, event hsm.Event) {
			configData.Store(event.Data.(*data))
		})),
		hsm.State("foo"),
		hsm.State("bar"),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.Transition(hsm.On("bar"), hsm.Source("bar"), hsm.Target("foo")),
	)
	ctx := context.Background()
	hsm.Start(ctx, &THSM{}, &model, hsm.Config{
		Data: &data{
			foo: "testing",
		},
	})
	if configData.Load().foo != "testing" {
		t.Fatal("config data is not correct", "config data", configData.Load())
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
	hsm.State("foo",
		hsm.Entry(noBehavior, noBehavior, noBehavior),
		hsm.Exit(noBehavior, noBehavior, noBehavior),
		hsm.Activity(noBehavior, noBehavior, noBehavior),
	),
	hsm.State("bar",
		hsm.Entry(noBehavior),
		hsm.Exit(noBehavior),
		hsm.Activity(noBehavior, noBehavior, noBehavior),
	),
	hsm.Transition(
		hsm.On("foo"),
		hsm.Source("foo"),
		hsm.Target("bar"),
		hsm.Effect(noBehavior, noBehavior, noBehavior),
	),
	hsm.Transition(
		hsm.On("bar"),
		hsm.Source("bar"),
		hsm.Target("foo"),
		hsm.Effect(noBehavior, noBehavior, noBehavior),
	),
	hsm.Initial(hsm.Target("foo"), hsm.Effect(noBehavior)),
)

func TestCompletionEvent(t *testing.T) {
	model := hsm.Define(
		"T",
		hsm.Initial(hsm.Target("a")),
		hsm.State("a", hsm.Transition(hsm.On("b"), hsm.Target("../b"))),
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
				hsm.On("c"),
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
							hsm.On("d"),
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
				hsm.On("d"),
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
	})
	<-done
	if sm.State() != "/d" {
		t.Fatalf("expected state \"/d\" got \"%s\"", sm.State())
	}

}

func TestMatch(t *testing.T) {
	const sample = "a/ab/abc/abcd/abcde/abcdef/abcdefg/abcdefgh/abcdefghi/abcdefghij/abcdefghijk/abcdefghijkl/abcdefghijklm/abcdefghijklmn/abcdefghijklmno/abcdefghijklmnop/abcdefghijklmnopq/abcdefghijklmnopqr/abcdefghijklmnopqrs/abcdefghijklmnopqrst/abcdefghijklmnopqrstu/abcdefghijklmnopqrstuv/abcdefghijklmnopqrstuvw/abcdefghijklmnopqrstuvwx/abcdefghijklmnopqrstuvwxy/abcdefghijklmnopqrstuvwxyz"
	if !hsm.Match(sample, "a/*/a*/abcde/*") {
		t.Fatal("expected match for a/*/a*/abcde/*")
	}
	if hsm.Match(sample, "a/*/a*/abcde/") {
		t.Fatal("expected no match for a/*/a*/abcde/")
	}
	if hsm.Match(sample, "a/*/a*/abcde/") {
		t.Fatal("expected no match for a/*/a*/abcde/")
	}
	if !hsm.Match("abc", "a*c") {
		t.Fatal("expected match for abc, a*c")
	}
	if !hsm.Match("abc", "*b*") {
		t.Fatal("expected match for abc, *b*")
	}
	if !hsm.Match("abc", "*c") {
		t.Fatal("expected match for abc, *c")
	}
	if !hsm.Match("abc", "a*") {
		t.Fatal("expected match for abc, a*")
	}
	if !hsm.Match("abc", "*") {
		t.Fatal("expected match for abc, *")
	}
	if !hsm.Match("", "*") {
		t.Fatal("expected match for '', *")
	}
	if !hsm.Match("a", "a") {
		t.Fatal("expected match for a, a")
	}
	if hsm.Match("a", "b") {
		t.Fatal("expected no match for a, b")
	}
	if hsm.Match("a", "") {
		t.Fatal("expected no match for a, ''")
	}
	if hsm.Match("", "a") {
		t.Fatal("expected no match for '', a")
	}
	if !hsm.Match("", "") {
		t.Fatal("expected match for '', ''")
	}
	if hsm.Match("abc", "a*d") {
		t.Fatal("expected no match for abc, a*d")
	}
	if !hsm.Match("abcdef", "ab*ef") {
		t.Fatal("expected match for abcdef, ab*ef")
	}
	if !hsm.Match("abcdef", "a*f") {
		t.Fatal("expected match for abcdef, a*f")
	}
	if !hsm.Match("/initialized/starting", "/initialized/starting") {
		t.Fatal("expected match for /initialized/starting, /initialized/starting")
	}
	if !hsm.Match("abcdef", "*f") {
		t.Fatal("expected match for abcdef, *f")
	}
	if !hsm.Match("abcdef", "a*") {
		t.Fatal("expected match for abcdef, a*")
	}
	if hsm.Match("abcdef", "a*g") {
		t.Fatal("expected no match for abcdef, a*g")
	}
	if !hsm.Match("zzabcdef", "*f") {
		t.Fatal("expected match for zzabcdef, *f")
	}
	if hsm.Match("zzabcdef", "b*") {
		t.Fatal("expected no match for zzabcdef, b*")
	}
	if !hsm.Match(sample, "*xyz") {
		t.Fatal("expected match for sample, *xyz")
	}
	if !hsm.Match(sample, "a*z") {
		t.Fatal("expected match for sample, a*z")
	}
	if !hsm.Match(sample, "a*x*z") {
		t.Fatal("expected match for sample, a*x*z")
	}
	if !hsm.Match(sample, "a*x*y*z") {
		t.Fatal("expected match for sample, a*x*y*z")
	}

}

func BenchmarkTransition(b *testing.B) {
	ctx := context.Background()
	var counter atomic.Int64
	behavior := func(ctx context.Context, hsm *THSM, event hsm.Event) {
		counter.Add(1)
	}
	var benchModel = hsm.Define(
		"BenchmarkExecutionHSM",
		hsm.State("foo",
			hsm.Entry(behavior),
			hsm.Exit(behavior),
			hsm.Activity(behavior),
		),
		hsm.State("bar",
			hsm.Entry(behavior),
			hsm.Exit(behavior),
			hsm.Activity(behavior),
		),
		hsm.Transition(
			hsm.On("foo"),
			hsm.Source("foo"),
			hsm.Target("bar"),
			hsm.Effect(behavior),
		),
		hsm.Transition(
			hsm.On("bar"),
			hsm.Source("bar"),
			hsm.Target("foo"),
			hsm.Effect(behavior),
		),
		hsm.Initial(hsm.Target("foo")),
	)
	fooEvent := hsm.Event{
		Name: "foo",
	}
	barEvent := hsm.Event{
		Name: "bar",
	}
	benchSM := hsm.Start(ctx, &THSM{}, &benchModel, hsm.Config{
		ActivityTimeout: 1 * time.Second,
	})
	b.ReportAllocs()
	b.ResetTimer()
	counter.Store(0)
	for i := 0; i < b.N; i++ {
		benchSM.Dispatch(ctx, fooEvent)
		benchSM.Dispatch(ctx, barEvent)
	}
	// slog.Info("stopping", "snapshot", hsm.TakeSnapshot(ctx, benchSM), "counter", counter.Load())
	<-hsm.Stop(ctx, benchSM)
	slog.Info("stopped", "snapshot", hsm.TakeSnapshot(ctx, benchSM), "counter", counter.Load(), "count", b.N*2*4)
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

func TestWhen(t *testing.T) {
	model := hsm.Define(
		"TestWhenHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo",
			hsm.Transition(
				hsm.When(func(ctx context.Context, hsm *THSM, event hsm.Event) <-chan struct{} {
					ch := make(chan struct{})
					go func() {
						time.Sleep(1 * time.Millisecond)
						close(ch)
					}()
					return ch
				}),
				hsm.Target("../bar"),
			),
		),
		hsm.State("bar"),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	time.Sleep(2 * time.Millisecond)
	if sm.State() != "/bar" {
		t.Fatalf("expected state to be bar, got %s", sm.State())
	}
}

func TestStop(t *testing.T) {
	sm := hsm.Start(context.Background(), &THSM{}, &benchModel)
	<-hsm.Stop(context.Background(), sm)
	instances, ok := hsm.InstancesFromContext(sm.Context())
	if !ok {
		t.Fatalf("expected instances to be non-nil")
	}
	if len(instances) != 0 {
		t.Fatalf("expected instances to be empty")
	}
}

func TestSnapshot(t *testing.T) {
	sm := hsm.Start(context.Background(), &THSM{}, &benchModel)
	snapshot := hsm.TakeSnapshot(context.Background(), sm)
	if snapshot.ID == "" {
		t.Fatalf("expected snapshot to have an ID")
	}
	if snapshot.State == "" {
		t.Fatalf("expected snapshot to have a state")
	}
	if snapshot.QueueLen != 0 {
		t.Fatalf("expected snapshot to have a queue length of 0")
	}
	if snapshot.QualifiedName == "" {
		t.Fatalf("expected snapshot to have a qualified name")
	}
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
	<-hsm.Stop(ctx, instance)
}

func TestRestart(t *testing.T) {
	model := hsm.Define(
		"TestRestartHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.State("bar", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
	<-sm.Dispatch(context.Background(), hsm.Event{Name: "foo"})
	if sm.State() != "/bar" {
		t.Fatalf("Expected state to be bar, got: %s", sm.State())
	}
	hsm.Restart(context.Background(), sm)
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
}

func TestDispatch(t *testing.T) {
	model := hsm.Define(
		"TestDispatchHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.State("bar", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
	)
	sm := hsm.Start(context.Background(), &THSM{}, &model)
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
	done := hsm.Dispatch(sm.Context(), hsm.Event{Name: "foo"})
	<-done
	if sm.State() != "/bar" {
		t.Fatalf("Expected state to be bar, got: %s", sm.State())
	}
	<-hsm.Stop(context.Background(), sm)
	select {
	case <-sm.Context().Done():
	default:
		t.Fatalf("Expected state machine to be done")
	}

}

func TestAfter(t *testing.T) {

	sm := hsm.Start(context.Background(), &THSM{}, &benchModel)
	if sm.State() != "/foo" {
		t.Fatalf("Expected state to be foo, got: %s", sm.State())
	}
	entered := hsm.AfterEntry(sm.Context(), sm, "/bar")
	exited := hsm.AfterExit(sm.Context(), sm, "/foo")
	dispatched := hsm.AfterDispatch(sm.Context(), sm, hsm.Event{Name: "foo"})
	processed := hsm.AfterProcess(sm.Context(), sm, hsm.Event{Name: "foo"})

	<-hsm.Dispatch(sm.Context(), hsm.Event{Name: "foo"})
	select {
	case <-dispatched:
	default:
		t.Fatalf("Expected dispatch to be called")
	}
	select {
	case <-entered:
	default:
		t.Fatalf("Expected entry to be called")
	}
	select {
	case <-exited:
	default:
		t.Fatalf("Expected exit to be called")
	}
	select {
	case <-processed:
	default:
		t.Fatalf("Expected processed to be called")
	}

}

func noGuard(ctx context.Context, hsm *THSM, event hsm.Event) bool {
	return true
}

func TestPropagate(t *testing.T) {
	model := hsm.Define(
		"TestPropagateHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.State("bar", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
	)
	sm1 := hsm.Start(context.Background(), &THSM{}, &model)
	sm2 := hsm.Start(sm1.Context(), &THSM{}, &model)
	<-hsm.Propagate(sm2.Context(), hsm.Event{Name: "foo"})
	if sm1.State() != "/bar" {
		t.Fatalf("Expected state to be bar, got: %s", sm1.State())
	}
}

func TestPropagateAll(t *testing.T) {
	model := hsm.Define(
		"TestPropagateAllHSM",
		hsm.Initial(hsm.Target("foo")),
		hsm.State("foo", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
		hsm.Transition(hsm.On("foo"), hsm.Source("foo"), hsm.Target("bar")),
		hsm.State("bar", hsm.Entry(noBehavior), hsm.Exit(noBehavior)),
	)
	instances := make([]hsm.Instance, 10)
	for i := 0; i < 10; i++ {
		var ctx context.Context
		if i == 0 {
			ctx = context.Background()
		} else {
			ctx = instances[i-1].Context()
		}
		instances[i] = hsm.Start(ctx, &THSM{}, &model)
	}
	<-hsm.PropagateAll(instances[len(instances)-1].Context(), hsm.Event{Name: "foo"})
	for i := range len(instances) - 1 {
		if instances[i].State() != "/bar" {
			t.Fatalf("Expected instance %d state to be bar, got: %s", i, instances[i].State())
		}
	}
}

func BenchmarkModel(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = hsm.Define(
			"TestHSM",
			hsm.State("s",
				hsm.Entry(noBehavior),
				hsm.Activity(noBehavior),
				hsm.Exit(noBehavior),
				hsm.State("s1",
					hsm.State("s11",
						hsm.Entry(noBehavior),
						hsm.Activity(noBehavior),
						hsm.Exit(noBehavior),
					),
					hsm.Initial(hsm.Target("s11"), hsm.Effect(noBehavior)),
					hsm.Exit(noBehavior),
					hsm.Entry(noBehavior),
					hsm.Activity(noBehavior),
					hsm.Transition(hsm.On("I"), hsm.Effect(noBehavior)),
					hsm.Transition(hsm.On("A"), hsm.Target("/s/s1"), hsm.Effect(noBehavior)),
				),
				hsm.Transition(hsm.On("D"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s1"), hsm.Effect(noBehavior), hsm.Guard(
					noGuard,
				)),
				hsm.Initial(hsm.Target("s1/s11"), hsm.Effect(noBehavior)),
				hsm.State("s2",
					hsm.Entry(noBehavior),
					hsm.Activity(noBehavior),
					hsm.Exit(noBehavior),
					hsm.State("s21",
						hsm.State("s211",
							hsm.Entry(noBehavior),
							hsm.Activity(noBehavior),
							hsm.Exit(noBehavior),
							hsm.Transition(hsm.On("G"), hsm.Target("/s/s1/s11"), hsm.Effect(noBehavior)),
						),
						hsm.Initial(hsm.Target("s211"), hsm.Effect(noBehavior)),
						hsm.Entry(noBehavior),
						hsm.Activity(noBehavior),
						hsm.Exit(noBehavior),
						hsm.Transition(hsm.On("A"), hsm.Target("/s/s2/s21")), // self transition
					),
					hsm.Initial(hsm.Target("s21/s211"), hsm.Effect(noBehavior)),
					hsm.Transition(hsm.On("C"), hsm.Target("/s/s1"), hsm.Effect(noBehavior)),
				),
				hsm.State("s3",
					hsm.Entry(noBehavior),
					hsm.Activity(noBehavior),
					hsm.Exit(noBehavior),
				),
				hsm.Transition(hsm.On(`*.P.*`), hsm.Effect(noBehavior)),
			),
			hsm.State("t",
				hsm.Entry(noBehavior),
				hsm.Activity(noBehavior),
				hsm.Exit(noBehavior),
				hsm.State(
					"u",
					hsm.Entry(noBehavior),
					hsm.Activity(noBehavior),
					hsm.Exit(noBehavior),
					hsm.Transition(
						hsm.On("u.t"),
						hsm.Target("/t"),
						hsm.Effect(noBehavior),
					),
				),
				hsm.Transition(
					hsm.On("X"),
					hsm.Target("/exit"),
					hsm.Effect(noBehavior),
				),
			),

			hsm.Final("exit"),
			hsm.Initial(
				hsm.Target(hsm.Choice(
					"initial_choice",
					hsm.Transition(hsm.Target("/s/s2")),
				)), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("D"), hsm.Source("/s/s1"), hsm.Target("/s"), hsm.Effect(noBehavior), hsm.Guard(
				noGuard,
			)),
			hsm.Transition("wildcard", hsm.On("abcd*"), hsm.Source("/s"), hsm.Target("/s")),
			hsm.Transition(hsm.On("D"), hsm.Source("/s"), hsm.Target("/s"), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("C"), hsm.Source("/s/s1"), hsm.Target("/s/s2"), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("E"), hsm.Source("/s"), hsm.Target("/s/s1/s11"), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("G"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s2/s21/s211"), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("I"), hsm.Source("/s"), hsm.Effect(noBehavior), hsm.Guard(
				noGuard,
			)),
			hsm.Transition(hsm.After(
				func(ctx context.Context, hsm *THSM, event hsm.Event) time.Duration {
					return time.Second * 2
				},
			), hsm.Source("/s/s2/s21/s211"), hsm.Target("/s/s1/s11"), hsm.Effect(noBehavior), hsm.Guard(
				noGuard,
			)),
			hsm.Transition(hsm.On("H"), hsm.Source("/s/s1/s11"), hsm.Target(
				hsm.Choice(
					hsm.Transition(hsm.Target("/s/s1"), hsm.Guard(
						noGuard,
					)),
					hsm.Transition(hsm.Target("/s/s2"), hsm.Effect(noBehavior)),
				),
			), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("J"), hsm.Source("/s/s2/s21/s211"), hsm.Target("/s/s1/s11"), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("K"), hsm.Source("/s/s1/s11"), hsm.Target("/s/s3"), hsm.Effect(noBehavior)),
			hsm.Transition(hsm.On("Z"), hsm.Effect(noBehavior), hsm.Source("/s/s3"), hsm.Target("/t/u")),
			hsm.Transition(hsm.On("X"), hsm.Effect(noBehavior), hsm.Source("/s/s3"), hsm.Target("/t/u")),
		)
	}
}
