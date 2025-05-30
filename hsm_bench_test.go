package hsm_test

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runpod/hsm/v2"
)

// BenchHSM is a wrapper for hsm.HSM for benchmarking purposes.
type BenchHSM struct {
	hsm.HSM
	effectCounter int64
}

func (b *BenchHSM) ResetCounters() {
	b.effectCounter = 0
}

// benchNoBehavior is a no-op action/activity function for benchmarks.
func benchNoBehavior(_ context.Context, _ *BenchHSM, _ hsm.Event) {
	// Do nothing
}

var activityWorkCounter atomic.Int64

// activityBehavior simulates a minimal activity that runs until context is cancelled.
func activityBehavior(ctx context.Context, b *BenchHSM, e hsm.Event) {
	for {
		select {
		case <-ctx.Done(): // Context is cancelled when state is exited
			return
		default:
			// Simulate minimal work by yielding the processor.
			runtime.Gosched()
			activityWorkCounter.Add(1) // Increment to show activity is doing work
		}
	}
}

// effectBehavior is an action function that performs a minimal operation.
func effectBehavior(_ context.Context, b *BenchHSM, _ hsm.Event) {
	b.effectCounter++
}

// benchNoGuard is a guard function that always returns true for benchmarks.
func benchNoGuard(_ context.Context, _ *BenchHSM, _ hsm.Event) bool {
	return true
}

// runHSMBenchmark is a helper to run a specific HSM benchmark scenario.
func runHSMBenchmark(b *testing.B, modelHsm hsm.Model, event1Name, event2Name string) {
	ctx := context.Background()
	instance := &BenchHSM{}

	// Pass model by reference to hsm.Start
	m := hsm.Start(ctx, instance, &modelHsm)
	if m == nil {
		b.Fatalf("Failed to create HSM: hsm.Start returned nil")
	}
	// It's important to stop the HSM to clean up resources, especially activities.
	defer hsm.Stop(ctx, m)

	event1 := hsm.Event{Name: event1Name}
	event2 := hsm.Event{Name: event2Name}

	// Fixed warmup iterations to match C++ (1000)
	warmupIterations := 1000
	for i := 0; i < warmupIterations; i++ {
		<-m.Dispatch(ctx, event1)
		<-m.Dispatch(ctx, event2)
	}

	instance.ResetCounters()
	activityWorkCounter.Store(0) // Reset global counters

	// Fixed benchmark iterations to match C++ (1000)
	benchmarkIterations := 1000

	// Manually measure time instead of using b.N
	start := time.Now()

	for i := 0; i < benchmarkIterations; i++ {
		<-m.Dispatch(ctx, event1)
		<-m.Dispatch(ctx, event2)
	}

	elapsed := time.Since(start)

	// Report using custom metrics
	totalTransitions := float64(benchmarkIterations) * 2.0 // Two transitions per iteration
	transitionsPerSec := totalTransitions / elapsed.Seconds()

	// Set b.N to the actual number of operations for proper reporting
	b.N = benchmarkIterations * 2

	// Report the time per operation (ns per transition)
	nsPerTransition := elapsed.Nanoseconds() / int64(b.N)
	b.ReportMetric(float64(nsPerTransition), "ns/op")
	b.ReportMetric(transitionsPerSec, "trans/sec")
	b.ReportMetric(float64(benchmarkIterations), "iterations")
}

// --- Scenario 1: Nested states (matching C++ benchmark) ---

// 1. Baseline: Nested states without entry, exit, or activities
func BenchmarkNestedStates_NoEntryExitActivity(b *testing.B) {
	model := hsm.Define(
		"TestHSM1",
		hsm.State("parent",
			hsm.State("child1"),
			hsm.State("child2"),
			hsm.Initial(hsm.Target("child1")),
			hsm.Transition(hsm.On("toChild2"), hsm.Source("child1"), hsm.Target("child2")),
			hsm.Transition(hsm.On("toChild1"), hsm.Source("child2"), hsm.Target("child1")),
		),
		hsm.Initial(hsm.Target("/parent")),
	)
	runHSMBenchmark(b, model, "toChild2", "toChild1")
}

// 1.a Nested states with entry functions only
func BenchmarkNestedStates_EntryOnly(b *testing.B) {
	model := hsm.Define(
		"TestHSM1a",
		hsm.State("parent",
			hsm.Entry(benchNoBehavior),
			hsm.State("child1", hsm.Entry(benchNoBehavior)),
			hsm.State("child2", hsm.Entry(benchNoBehavior)),
			hsm.Initial(hsm.Target("child1")),
			hsm.Transition(hsm.On("toChild2"), hsm.Source("child1"), hsm.Target("child2")),
			hsm.Transition(hsm.On("toChild1"), hsm.Source("child2"), hsm.Target("child1")),
		),
		hsm.Initial(hsm.Target("/parent")),
	)
	runHSMBenchmark(b, model, "toChild2", "toChild1")
}

// 1.b Nested states with entry and activity functions
func BenchmarkNestedStates_EntryActivity(b *testing.B) {
	model := hsm.Define(
		"TestHSM1b",
		hsm.State("parent",
			hsm.Entry(benchNoBehavior),
			hsm.Activity(activityBehavior),
			hsm.State("child1",
				hsm.Entry(benchNoBehavior),
				hsm.Activity(activityBehavior),
			),
			hsm.State("child2",
				hsm.Entry(benchNoBehavior),
				hsm.Activity(activityBehavior),
			),
			hsm.Initial(hsm.Target("child1")),
			hsm.Transition(hsm.On("toChild2"), hsm.Source("child1"), hsm.Target("child2")),
			hsm.Transition(hsm.On("toChild1"), hsm.Source("child2"), hsm.Target("child1")),
		),
		hsm.Initial(hsm.Target("/parent")),
	)
	runHSMBenchmark(b, model, "toChild2", "toChild1")
}

// 1.c Nested states with entry, exit, and activity functions
func BenchmarkNestedStates_EntryExitActivity(b *testing.B) {
	model := hsm.Define(
		"TestHSM1c",
		hsm.State("parent",
			hsm.Entry(benchNoBehavior),
			hsm.Exit(benchNoBehavior),
			hsm.Activity(activityBehavior),
			hsm.State("child1",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
				hsm.Activity(activityBehavior),
			),
			hsm.State("child2",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
				hsm.Activity(activityBehavior),
			),
			hsm.Initial(hsm.Target("child1")),
			hsm.Transition(hsm.On("toChild2"), hsm.Source("child1"), hsm.Target("child2")),
			hsm.Transition(hsm.On("toChild1"), hsm.Source("child2"), hsm.Target("child1")),
		),
		hsm.Initial(hsm.Target("/parent")),
	)
	runHSMBenchmark(b, model, "toChild2", "toChild1")
}

// 1.d Nested states with entry, exit, activity functions and transition effects
func BenchmarkNestedStates_EntryExitActivityEffect(b *testing.B) {
	model := hsm.Define(
		"TestHSM1d",
		hsm.State("parent",
			hsm.Entry(benchNoBehavior),
			hsm.Exit(benchNoBehavior),
			hsm.Activity(activityBehavior),
			hsm.State("child1",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
				hsm.Activity(activityBehavior),
			),
			hsm.State("child2",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
				hsm.Activity(activityBehavior),
			),
			hsm.Initial(hsm.Target("child1")),
			hsm.Transition(hsm.On("toChild2"), hsm.Source("child1"), hsm.Target("child2"), hsm.Effect(effectBehavior)),
			hsm.Transition(hsm.On("toChild1"), hsm.Source("child2"), hsm.Target("child1"), hsm.Effect(effectBehavior)),
		),
		hsm.Initial(hsm.Target("/parent")),
	)
	runHSMBenchmark(b, model, "toChild2", "toChild1")
}

// Additional test: Deep nesting (3 levels)
func BenchmarkDeepNesting3Levels_EntryExit(b *testing.B) {
	model := hsm.Define(
		"TestHSMDeep",
		hsm.State("level1",
			hsm.Entry(benchNoBehavior),
			hsm.Exit(benchNoBehavior),
			hsm.State("level2",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
				hsm.State("level3a",
					hsm.Entry(benchNoBehavior),
					hsm.Exit(benchNoBehavior),
				),
				hsm.State("level3b",
					hsm.Entry(benchNoBehavior),
					hsm.Exit(benchNoBehavior),
				),
				hsm.Initial(hsm.Target("level3a")),
				hsm.Transition(hsm.On("toLevel3b"), hsm.Source("level3a"), hsm.Target("level3b")),
				hsm.Transition(hsm.On("toLevel3a"), hsm.Source("level3b"), hsm.Target("level3a")),
			),
			hsm.Initial(hsm.Target("level2")),
		),
		hsm.Initial(hsm.Target("/level1")),
	)
	runHSMBenchmark(b, model, "toLevel3b", "toLevel3a")
}

// Test exiting and entering nested states from outside
func BenchmarkCrossHierarchyTransitions_EntryExit(b *testing.B) {
	model := hsm.Define(
		"TestHSMCrossHierarchy",
		hsm.State("parent1",
			hsm.Entry(benchNoBehavior),
			hsm.Exit(benchNoBehavior),
			hsm.State("child1",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
			),
			hsm.Initial(hsm.Target("child1")),
		),
		hsm.State("parent2",
			hsm.Entry(benchNoBehavior),
			hsm.Exit(benchNoBehavior),
			hsm.State("child2",
				hsm.Entry(benchNoBehavior),
				hsm.Exit(benchNoBehavior),
			),
			hsm.Initial(hsm.Target("child2")),
		),
		hsm.Transition(hsm.On("toParent2"), hsm.Source("parent1"), hsm.Target("parent2")),
		hsm.Transition(hsm.On("toParent1"), hsm.Source("parent2"), hsm.Target("parent1")),
		hsm.Initial(hsm.Target("/parent1")),
	)
	runHSMBenchmark(b, model, "toParent2", "toParent1")
}
