package hsm

import (
	"context"
	"sync"
	"testing"
	"time"
)

type dispatchMachine struct{ HSM }

// Acknowledging an event from its effect lets the next dispatch race with the
// processor's final empty-queue check. Every queued event must be processed
// without requiring an unrelated later dispatch to wake the processor.
func TestDispatchDoesNotStrandQueuedEvents(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "live"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			model := Define("dispatch",
				Initial(Target("idle")),
				State("idle", Transition(On("tick"),
					Effect(func(_ context.Context, _ *dispatchMachine, event Event) {
						event.Data.(chan struct{}) <- struct{}{}
					}))),
			)
			if canceled {
				cancel()
			}
			var workers sync.WaitGroup
			for range 8 {
				workers.Add(1)
				go func() {
					defer workers.Done()
					sm := Start(ctx, &dispatchMachine{}, &model)
					defer func() { <-Stop(context.Background(), sm) }()
					ack := make(chan struct{}, 1)
					var completions []<-chan struct{}
					for i := range 10000 {
						completions = append(completions, sm.Dispatch(ctx, Event{Name: "tick", Data: ack}))
						select {
						case <-ack:
						case <-time.After(time.Second):
							t.Errorf("event %d stranded: state=%s queued=%d", i,
								sm.State(), sm.takeSnapshot().QueueLen)
							return
						}
					}
					// Wait after the burst so completion checks do not serialize away
					// the dispatch/processor handoff race exercised above.
					deadline := time.NewTimer(time.Second)
					defer deadline.Stop()
					for i, completion := range completions {
						select {
						case <-completion:
						case <-deadline.C:
							t.Errorf("completion channel for event %d never closed", i)
							return
						}
					}
				}()
			}
			workers.Wait()
		})
	}
}

func TestDispatchWithOnlyDeferredEventsBecomesIdle(t *testing.T) {
	ctx := context.Background()
	model := Define("deferred",
		Initial(Target("idle")),
		State("idle", Defer("tick")),
	)
	sm := Start(ctx, &dispatchMachine{}, &model)
	defer func() { <-Stop(ctx, sm) }()
	select {
	case <-sm.Dispatch(ctx, Event{Name: "tick"}):
	case <-time.After(time.Second):
		t.Fatal("processor did not become idle with only deferred events")
	}
	if snapshot := sm.takeSnapshot(); snapshot.State != "/idle" || snapshot.QueueLen != 1 {
		t.Fatalf("deferred event was not retained in idle state: %+v", snapshot)
	}
}
