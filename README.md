# hsm [![PkgGoDev](https://pkg.go.dev/badge/github.com/runpod/hsm)](https://pkg.go.dev/github.com/runpod/hsm)

Package go-hsm provides a powerful hierarchical state machine (HSM) implementation for Go. State machines help manage complex application states and transitions in a clear, maintainable way.

## Installation

```bash
go get github.com/runpod/hsm/v2
```

## Key Features

- Hierarchical state organization
- Entry, exit, and multiple activity actions for states
- Guard conditions and transition effects
- Event-driven transitions (`hsm.On`)
- Time-based transitions (`hsm.After`, `hsm.Every`)
- Concurrent state execution (`hsm.Activity`)
- Event queuing with completion event priority
- Multiple state machine instances with broadcast support (`hsm.DispatchAll`, `hsm.DispatchTo`)
- Event completion tracking (via `Dispatch` return channel)
- Event deferral support (`hsm.Defer`)
- State machine-level activity actions (`hsm.Activity` within `hsm.Define`)
- Automatic termination with final states (`hsm.Final`)
- Pattern matching for event names and state machine IDs (`hsm.Match`, wildcards in `hsm.On`, `hsm.DispatchTo`)
- Event propagation between state machines (`hsm.Propagate`, `hsm.PropagateAll`)
- Snapshotting (`hsm.TakeSnapshot`)

## Core Concepts

A state machine is a computational model that defines how a system behaves and transitions between different states. Here are key concepts:

- **State**: A condition or situation of the system at a specific moment. For example, a traffic light can be in states like "red", "yellow", or "green".
- **Event**: A trigger that can cause the system to change states. Events can be external (user actions) or internal (timeouts).
- **Transition**: A change from one state to another in response to an event.
- **Guard**: A condition that must be true for a transition to occur (`hsm.Guard`).
- **Action**: Code that executes when entering/exiting states or during transitions (`hsm.Entry`, `hsm.Exit`, `hsm.Effect`).
- **Hierarchical States**: States that contain other states, allowing for complex behavior modeling with inheritance.
- **Initial State**: The starting state when the machine begins execution (`hsm.Initial`).
- **Final State**: A state indicating the machine has completed its purpose (`hsm.Final`).

### Why Use State Machines?

State machines are particularly useful for:

- Managing complex application flows
- Handling user interactions
- Implementing business processes
- Controlling system behavior
- Modeling game logic
- Managing workflow states

## Usage Guide

### Basic State Machine Structure

All state machines must embed the `hsm.HSM` struct and can add their own fields:

```go
type MyHSM struct {
    hsm.HSM // Required embedded struct
    counter int
    status  string
}
```

### Creating and Starting a State Machine

```go
import (
	"context"
	"log/slog"
	"time"

	"github.com/runpod/hsm/v2"
)

// Define your state machine type
type MyHSM struct {
    hsm.HSM
    counter int
}

// Create the state machine model
model := hsm.Define(
    "example",
    hsm.State("foo"),
    hsm.State("bar"),
    hsm.Transition(
        hsm.On("moveToBar"), // Use hsm.On to specify event triggers
        hsm.Source("foo"),
        hsm.Target("bar"),
        hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        	slog.Info("Transitioning to bar")
        }),
    ),
    hsm.Initial(hsm.Target("foo")) // Specify initial target state
)

// Create and start the state machine
sm := hsm.Start(context.Background(), &MyHSM{}, &model)

// Create event
event := hsm.Event{
    Name: "moveToBar",
}

// Dispatch event and wait for completion using the returned channel
done := sm.Dispatch(context.Background(), event)
<-done // The channel closes when the event processing is complete
```

### State Actions

States can have multiple types of actions:

```go
type MyHSM struct {
    hsm.HSM
    status string
}

model := hsm.Define(
    "stateActionsExample",
    hsm.Initial(hsm.Target("active")), // Need an initial state for a valid model
    hsm.State("active",
        // Entry action - runs once when state is entered
        hsm.Entry(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
            slog.Info("Entering active state")
        }),
    ),
)
```

### State Machine Actions

The state machine itself can have activity actions defined at the top level:

```go
model := hsm.Define(
    "example",
    // Activity action for the entire state machine
    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        // This activity's context (ctx) is cancelled only when the state machine stops
        ticker := time.NewTicker(5 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                 slog.Info("State machine background activity stopping")
                return
            case <-ticker.C:
                slog.Info("State machine background activity tick")
            }
        }
    }),

    // States and transitions...
    hsm.State("idle"),
    hsm.Initial(hsm.Target("idle")),
)
```

### Logging Support

The HSM package uses the standard `log/slog` package. You can use `slog` directly within your action functions (Entry, Exit, Effect, Activity, Guard). The state machine itself does not require a specific logger configuration, but you can manage the global `slog` logger as needed for your application.

```go
import "log/slog"

// Use slog within actions
hsm.State("active",
    hsm.Entry(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        slog.Info("Entering active state", "event", event.Name, "id", hsm.ID())
    }),
    hsm.Exit(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
         slog.Info("Exiting active state", "event", event.Name)
    }),
     hsm.Transition(
        hsm.On("someEvent"),
        hsm.Target("nextState"),
        hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
            slog.Debug("Effect executed", "data", event.Data)
        }),
    ),
)

// Configure the global slog logger if desired (e.g., in your main function)
// textHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
// slog.SetDefault(slog.New(textHandler))
```

### State Machine Lifecycle Management

Additional lifecycle management features:

```go
// Restart a state machine (returns to initial state, re-runs initial transition)
// Returns a channel that closes when the restart process completes.
restartDone := hsm.Restart(context.Background(), sm)
<-restartDone

// Stop a state machine gracefully (cancels activities, processes final exits)
// Returns a channel that closes when the stop process completes.
stopDone := hsm.Stop(context.Background(), sm)
<-stopDone  // Wait for completion

// Take a snapshot of the current state machine state
// The exact return type might vary, consult the implementation.
// snapshot := hsm.TakeSnapshot(sm)
// slog.Info("HSM Snapshot", "state", snapshot.CurrentState, "internalData", snapshot.Data) // Example usage

// Get the state machine's root context
ctx := sm.Context()

// Get the state machine's assigned ID
id := hsm.ID(sm)

// Get the state machine's qualified name (from Define or Config)
qName := hsm.QualifiedName(sm)

// Get the state machine's base name (from Define or Config)
name := hsm.Name(sm)

```

### Event Dispatch Methods

Multiple ways to dispatch events:

```go
// Direct dispatch to a specific state machine instance
// Returns a channel that closes when event processing is complete.
done := sm.Dispatch(context.Background(), hsm.Event{Name: "myEvent"})
<-done  // Wait for completion

// Dispatch through context (useful if you have the context but not the instance)
// Note: Requires the HSM instance to be associated with the context (done automatically by Start)
done = hsm.Dispatch(sm.Context(), hsm.Event{Name: "myEvent"})
<-done

// Broadcast to all state machines associated with the context
// Returns a channel that closes when all instances have completed processing.
done = hsm.DispatchAll(sm.Context(), hsm.Event{Name: "globalEvent"})
<-done

// Dispatch to specific state machine(s) by ID pattern (wildcards allowed)
// Returns a channel that closes when all targeted instances have completed processing.
done = hsm.DispatchTo(sm.Context(), hsm.Event{Name: "targetedEvent"}, "machine-1", "service-*")
<-done

// Propagate event to the *immediately preceding* state machine in the creation chain (if any)
// Returns a channel that closes when the target instance completes processing.
done = hsm.Propagate(sm.Context(), hsm.Event{Name: "propagateEvent"})
<-done

// Propagate event to *all* preceding state machines in the creation chain
// Returns a channel that closes when all targeted instances complete processing.
done = hsm.PropagateAll(sm.Context(), hsm.Event{Name: "propagateAllEvent"})
<-done
```

### Pattern Matching

Support for wildcard pattern matching in event names (`hsm.On`) and state machine IDs (`hsm.DispatchTo`). The `hsm.Match` function allows explicit pattern checks.

```go
// Match state/event names against patterns
matched, _ := hsm.Match("/state/substate", "/state/*") // Using path.Match - true
matched, _ = hsm.Match("/state/sub", "/state/*")       // true
matched, _ = hsm.Match("/foo/bar/baz", "/foo/bar")    // false

// Use wildcards in event triggers (uses path.Match internally)
hsm.Transition(
    hsm.On("*.event.*\"),  // Matches events like "req.event.id", "res.event.name"
    hsm.Source("active"),
    hsm.Target("next")
)

hsm.Transition(
    hsm.On("data?update"), // Matches "data1update", "data2update", but not "dataupdate" or "data12update"
    hsm.Source("processing"),
    hsm.Target("complete")
)

// Dispatch using ID patterns
hsm.DispatchTo(ctx, event, "worker-*", "monitor?") // Dispatch to IDs like worker-1, worker-2, monitorA, monitorB
```

### Event Deferral

States can defer specific events (by name pattern) to be processed only after the state machine transitions _out_ of the deferring state.

```go
model := hsm.Define(
    "deferExample",
    hsm.Initial(hsm.Target("busy")), // Need an initial state
    hsm.State("busy",
        // Defer any event named "update" or matching "config.*"
        hsm.Defer("update", "config.*"),
        hsm.Transition(
            hsm.On("complete"),
            hsm.Target("idle")
            // When transitioning to "idle", any deferred "update" or "config.*"
            // events in the queue will be re-processed immediately.
        )
    ),
    hsm.State("idle"), // Need the target state
)
```

### Event Listeners

listen for specific state entries, exits, event dispatches, and processing completions for a given state machine instance.

```go
// Listen for entry into the "/active" state
entry := hsm.AfterEntry(sm.Context(), sm, "/active")
// Listen for exit from the "/idle" state
exit := hsm.AfterExit(sm.Context(), sm, "/idle")
// Listen for the dispatch of an event named "myEvent"
dispatched := hsm.AfterDispatch(sm.Context(), sm, hsm.Event{Name: "myEvent"})
// Listen for the completion of processing for an event named "myEvent"
processed := hsm.AfterProcess(sm.Context(), sm, hsm.Event{Name: "myEvent"})

// Example usage: Wait for dispatch
select {
case <-dispatch:
    slog.Info("myEvent was dispatched")
case <-time.After(1*time.Second):
	slog.Warn("Timeout waiting for dispatch")
}

// Example usage: Wait for state entry
select {
case <-entry:
    slog.Info("Entered /active state")
case <-time.After(1*time.Second):
	slog.Warn("Timeout waiting for entry")
}


// Example usage: Wait for state exit
select {
case <-exit:
    slog.Info("Exited /idle state")
case <-time.After(1*time.Second):
	slog.Warn("Timeout waiting for exit")
}

// Example usage: Wait for event processing completion
select {
case <-processing:
    slog.Info("myEvent processing completed")
case <-time.After(1*time.Second):
	slog.Warn("Timeout waiting for processing")
}

```

_Note: OnceX methods are one-shot. They must be created to handle another event._

### Final States

A final state defined at the top level (`/`) using `hsm.Final` will automatically stop the state machine when entered. Entering a final state within a composite state generates a completion event for the parent state but does not stop the entire machine.

```go
model := hsm.Define(
    "example",
    hsm.State("active"),
    hsm.Final("finished"),  // This is a top-level final state
    hsm.Transition(
        hsm.On("complete"),
        hsm.Source("active"),
        hsm.Target("finished") // Transitioning here will stop the state machine
    ),
    hsm.Initial(hsm.Target("active"))
)
```

### Choice States

Choice pseudo-states allow dynamic branching based on guard conditions evaluated at runtime. Transitions _out_ of a choice state are evaluated in order, and the first one whose guard passes (or a transition with no guard) is taken.

```go
type MyHSM struct {
    hsm.HSM
    score int
}

hsm.State("processing",
    hsm.Transition(
        hsm.On("decide"),
        hsm.Target( // Target the choice pseudo-state
            hsm.Choice("approvalChoice", // Optional name for the choice state
                // First transition: Check score > 700
                hsm.Transition(
                    hsm.Target("../approved"), // Target relative to processing state
                    hsm.Guard(func(ctx context.Context, hsm *MyHSM, event hsm.Event) bool {
                        return hsm.score > 700
                    }),
                    hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) { slog.Info("Choice: Approved") }),
                ),
                // Second transition: Check score > 500 (only checked if first guard failed)
                hsm.Transition(
                    hsm.Target("../review"),
                     hsm.Guard(func(ctx context.Context, hsm *MyHSM, event hsm.Event) bool {
                        return hsm.score > 500
                    }),
                     hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) { slog.Info("Choice: Review") }),
                ),
                // Default transition (taken if no preceding guards passed)
                hsm.Transition(
                    hsm.Target("../rejected"),
                     hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) { slog.Info("Choice: Rejected") }),
                ),
            ),
        ),
        hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) { slog.Info("Transitioning to choice") }),
    ),
    hsm.State("approved"),
    hsm.State("review"),
    hsm.State("rejected")
)
```

### Event Broadcasting

Multiple state machine instances can be associated via their context. `hsm.DispatchAll` sends an event to all instances, and `hsm.DispatchTo` sends to instances matching ID patterns.

```go
type MyHSM struct {
    hsm.HSM
    // No specific id field needed here unless used internally
}

// Start first machine, gets its own context derived from background
sm1 := hsm.Start(context.Background(), &MyHSM{}, &model, hsm.Config{Id: "sm1"})

// Start second machine, passing sm1's context. This links them.
sm2 := hsm.Start(sm1.Context(), &MyHSM{}, &model, hsm.Config{Id: "sm2"})

// Start third machine, also linked via sm1's context
sm3 := hsm.Start(sm1.Context(), &MyHSM{}, &model, hsm.Config{Id: "another"})


// Dispatch event to all state machines (sm1, sm2, sm3)
<-hsm.DispatchAll(sm1.Context(), hsm.Event{Name: "globalEvent"}) // Use context from any linked SM

// Dispatch event to state machines with IDs matching patterns "sm*" and "another"
<-hsm.DispatchTo(sm2.Context(), hsm.Event{Name: "matchEvent"}, "sm*", "another") // sm1, sm2, sm3 targeted
```

### Transitions

Transitions define how states change in response to events (`hsm.On`). They can optionally specify `hsm.Source` (defaults to containing state), `hsm.Target` (required for external/local transitions, omitted for internal), `hsm.Guard`, and `hsm.Effect`.

```go
type MyHSM struct {
    hsm.HSM
    data []string
}

hsm.State("draft",
    hsm.Transition(
        hsm.On("submit"), // Event trigger
        // hsm.Source("draft") // Optional, defaults to "draft"
        hsm.Target("review"), // Target state
        hsm.Guard(func(ctx context.Context, hsm *MyHSM, event hsm.Event) bool {
            // Condition: only transition if data is not empty
            return len(hsm.data) > 0
        }),
        hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
            // Action executed during transition
            slog.Info("Transitioning from draft to review", "dataSize", len(hsm.data))
        }),
    ),
    // Internal transition (no target, stays in draft state)
    hsm.Transition(
        hsm.On("update"),
        hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
            slog.Info("Updating draft", "eventData", event.Data)
            // Modify hsm.data based on event.Data
        }),
    ),
)
hsm.State("review")

```

### Hierarchical States

States can be nested within other states. This allows for inheriting transitions, actions, and defining composite states with their own initial states.

```go
type MachineHSM struct {
    hsm.HSM
    status string
}

model := hsm.Define(
    "machine",
    hsm.State("operational", // Parent state
        hsm.Entry(func(ctx context.Context, hsm *MachineHSM, event hsm.Event) { slog.Info("Entering Operational") }),
        hsm.State("idle"), // Child state 1
        hsm.State("running", // Child state 2
             hsm.Entry(func(ctx context.Context, hsm *MachineHSM, event hsm.Event) { hsm.status = "running" }),
             hsm.Transition(hsm.On("stop"), hsm.Target("../idle")), // Transition within parent
        ),
        hsm.Initial(hsm.Target("idle")), // Initial state for "operational"
        hsm.Transition( // Transition defined in parent, applies to children
            hsm.On("start"),
            hsm.Source("idle"), // Can specify source within parent
            hsm.Target("running"),
            hsm.Guard(func(ctx context.Context, hsm *MachineHSM, event hsm.Event) bool { return hsm.status != "error"}),
        ),
         hsm.Transition( // Transition from parent to sibling state
            hsm.On("fail"),
            hsm.Target("/maintenance"), // Target outside parent
        ),
    ),
    hsm.State("maintenance"),
    hsm.Initial(hsm.Target("operational")) // Initial state for the whole machine
)
```

### Time-Based Transitions

Create transitions that occur after a dynamic time delay (`hsm.After`) or at regular dynamic intervals (`hsm.Every`). These implicitly define an activity in the source state.

```go
type TimerHSM struct {
    hsm.HSM
    timeout time.Duration
    interval time.Duration
}

hsm.State("active",
    // One-time delayed transition after hsm.timeout duration
    hsm.Transition(
        hsm.After(func(ctx context.Context, hsm *TimerHSM, event hsm.Event) time.Duration {
            return hsm.timeout // Dynamically return the duration
        }),
        // Source defaults to "active"
        hsm.Target("timeout"),
        hsm.Effect(func(ctx context.Context, hsm *TimerHSM, event hsm.Event) { slog.Info("Timeout occurred")}),
    ),

    // Recurring internal transition every hsm.interval
    hsm.Transition(
        hsm.Every(func(ctx context.Context, hsm *TimerHSM, event hsm.Event) time.Duration {
            return hsm.interval // Dynamically return the interval
        }),
        // Source defaults to "active", no Target makes it internal
        hsm.Effect(func(ctx context.Context, hsm *TimerHSM, event hsm.Event) {
            slog.Info("Recurring action executed")
            // Perform periodic task
        }),
    ),
)
hsm.State("timeout")

```

### Context Usage in Activities

Activities (`hsm.Activity`) receive a `context.Context` that is cancelled when the state they are defined in is exited. For operations that need to survive state changes, use the state machine's root context obtained via `hsm.Context()`.

```go
type MyHSM struct {
    hsm.HSM
    data chan string
}

hsm.State("processing",
    // Activity bound to state lifetime
    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        // This goroutine's context 'ctx' will be cancelled when leaving "processing" state
        slog.Info("Starting state-bound activity")
        for {
            select {
            case <-ctx.Done():
                slog.Info("State-bound activity cancelled", "reason", ctx.Err())
                return
            case data := <-hsm.data: // Example: processing data
                slog.Info("State-bound processed:", data)
                 time.Sleep(50 * time.Millisecond) // Simulate work
            }
        }
    }),

    // Activity using state machine's root context
    hsm.Activity(func(stateCtx context.Context, hsm *MyHSM, event hsm.Event) {
        // Use sm.Context() for operations that should continue across state changes
        smCtx := hsm.Context() // Get the root context
        slog.Info("Starting long-running activity using root context")
        go func() { // Launch a separate goroutine managed by the root context
            for {
                select {
                case <-smCtx.Done(): // Cancelled only when hsm.Stop() is called
                    slog.Info("Long-running activity cancelled via root context", "reason", smCtx.Err())
                    return
                case data := <-hsm.data: // Example: processing data
                    slog.Info("Long-running process:", data)
                    time.Sleep(100 * time.Millisecond) // Simulate work
                }
            }
        }()
    }),
    hsm.Transition(hsm.On("finish"), hsm.Target("done")),
)
hsm.State("done")
```

_Note: Be careful when using the state machine's root context in activities, as these operations will continue running until the state machine is explicitly stopped (`hsm.Stop`), potentially consuming resources even if the originating state is no longer active._

### Event Completion Tracking

The `hsm.Dispatch` methods return a `<-chan struct{}`. This channel is closed _after_ the dispatched event and any resulting synchronous actions (entry/exit/effects) have been fully processed. This allows callers to wait for completion.

```go
type ProcessHSM struct {
    hsm.HSM
    result string
}

// Assume 'sm' is a running ProcessHSM instance
// Assume 'payload' is some data for the event

// Create event
event := hsm.Event{
    Name: "process",
    Data: payload,
}

// Dispatch event and get completion channel
slog.Info("Dispatching 'process' event")
done := sm.Dispatch(context.Background(), event)

// Wait for processing to complete or timeout
select {
case <-done:
    // This block executes after the 'process' event and any triggered
    // synchronous effects/entries/exits are finished.
    slog.Info("Event 'process' processing completed", "result", sm.result)
    // You can now safely access results modified by the event processing.
case <-time.After(5 * time.Second):
    slog.Error("Timeout waiting for 'process' event processing")
}
```

### Obtaining Instances from Context

Retrieve the state machine instance (`hsm.Instance`) or all instances associated with a given context. This is useful in shared code or middleware where you might only have the context.

```go
// Function that might receive a context potentially associated with an HSM
func handleRequest(ctx context.Context) {
    // Get the specific state machine instance associated with this context (if any)
    if sm, ok := hsm.FromContext(ctx); ok {
        slog.Info("HSM found in context", "id", hsm.ID(), "state", sm.State())
        // You can now interact with 'sm', e.g., dispatch events
        // sm.Dispatch(ctx, hsm.Event{Name: "requestReceived"})
    } else {
         slog.Warn("No HSM instance found in context")
    }

    // Get all state machine instances associated with the context
    if instances, ok := hsm.InstancesFromContext(ctx); ok {
        slog.Info("Found instances in context", "count", len(instances))
        for _, instance := range instances {
            slog.Debug("Instance details", "id", hsm.ID(instance), "state", instance.State())
        }
    }
}

// Example usage: Call handleRequest with the HSM's context
// sm := hsm.Start(...)
// go handleRequest(sm.Context())
```

### Configuration on Start

Configure a state machine instance during `hsm.Start` using `hsm.Config`.

```go
type InitData struct {
	value string
	count int
}

type MyHSM struct {
    hsm.HSM
    initialValue string
}

// Define the model separately first
model := hsm.Define(
	"configuredHSM",
	hsm.Initial(
		hsm.Target("active"),
		hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
			// The initial transition receives Config.Data in event.Data
			if data, ok := event.Data.(*InitData); ok {
				slog.Info("Initializing HSM from Config.Data", "value", data.value, "count", data.count)
				hsm.initialValue = data.value
			} else {
				slog.Warn("Initial transition did not receive expected InitData type")
			}
		}),
	),
	hsm.State("active"),
)

// Pass configuration and initial data when starting
sm := hsm.Start(ctx, &MyHSM{}, &model, hsm.Config{
    Id: "my-unique-id-123", // Assign a specific ID
    Name: "MyConfiguredStateMachine", // Assign a name
    ActivityTimeout: time.Second * 1, // Timeout for activity termination on exit (default: 1ms)
    Data: &InitData{value: "initial-value", count: 10}, // Pass arbitrary data to the initial transition
})

// Access Config.Data in the initial transition's effect
// model := hsm.Define(
// 	"configuredHSM",
// 	hsm.Initial(
// 		hsm.Target("active"),
// 		hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
// 			// The initial transition receives Config.Data in event.Data
// 			if data, ok := event.Data.(*InitData); ok {
// 				slog.Info("Initializing HSM from Config.Data", "value", data.value, "count", data.count)
// 				hsm.initialValue = data.value
// 			} else {
// 				slog.Warn("Initial transition did not receive expected InitData type")
// 			}
// 		}),
// 	),
// 	hsm.State("active"),
// )

```

## Roadmap

Current and planned features:

- [x] Event-driven transitions (`hsm.On`)
- [x] Time-based transitions (`hsm.After`, `hsm.Every`)
- [x] Hierarchical state nesting
- [x] Entry/exit/activity actions (`hsm.Entry`, `hsm.Exit`, `hsm.Activity`)
- [x] Guard conditions (`hsm.Guard`)
- [x] Transition effects (`hsm.Effect`)
- [x] Choice pseudo-states (`hsm.Choice`)
- [x] Event broadcasting (`hsm.DispatchAll`) and targeted dispatch (`hsm.DispatchTo`)
- [x] Concurrent activities (`hsm.Activity`)
- [x] Pattern matching for event names and state machine IDs (`hsm.Match`, wildcards)
- [x] Event propagation between machines (`hsm.Propagate`, `hsm.PropagateAll`)
- [x] Event Deferral (`hsm.Defer`)
- [x] Final States (`hsm.Final`) & Automatic Termination
- [x] Instance management via Context (`hsm.FromContext`, `hsm.InstancesFromContext`)
- [x] Lifecycle management (`hsm.Start`, `hsm.Stop`, `hsm.Restart`)
- [ ] Scheduled transitions (at specific dates/times, e.g., `hsm.At`)
  ```go
  // Planned API
  hsm.Transition(
      hsm.At(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)),
      hsm.Source("active"),
      hsm.Target("newYear")
  )
  ```
- [ ] History support (shallow and deep pseudo-states to return to last active substate)
  ```go
  // Planned API
  hsm.State("parent",
      hsm.History(), // Shallow history pseudo-state H
      hsm.DeepHistory(), // Deep history pseudo-state H*
      hsm.State("child1"),
      hsm.State("child2", hsm.State("grandchild")),
      hsm.Transition(hsm.On("resume"), hsm.Target("H")), // Transition to H restores child1 or child2
      hsm.Transition(hsm.On("deepResume"), hsm.Target("H*")) // Transition to H* restores grandchild if active
  )
  ```

## Learn More

For deeper understanding of state machines:

- [UML State Machine Diagrams](https://www.uml-diagrams.org/state-machine-diagrams.html)
- [Statecharts: A Visual Formalism](https://www.sciencedirect.com/science/article/pii/0167642387900359) - The seminal paper by David Harel
- [State Pattern](https://refactoring.guru/design-patterns/state) - Design pattern implementation
- [State Charts](https://statecharts.dev/) - A comprehensive guide to statecharts

## License

MIT - See LICENSE file

## Contributing

Contributions are welcome! Please ensure:

- Tests are included
- Code is well documented
- Changes maintain backward compatibility
- Follow Go best practices
- Signature changes follow the context+hsm+event pattern where applicable
