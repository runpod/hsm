# hsm [![PkgGoDev](https://pkg.go.dev/badge/github.com/runpod/hsm)](https://pkg.go.dev/github.com/runpod/hsm)

Package go-hsm provides a powerful hierarchical state machine (HSM) implementation for Go. State machines help manage complex application states and transitions in a clear, maintainable way.

## Installation

```bash
go get github.com/runpod/hsm
```

## Key Features

- Hierarchical state organization
- Entry, exit, and multiple activity actions for states
- Guard conditions and transition effects
- Event-driven transitions
- Time-based transitions
- Concurrent state execution
- Event queuing with completion event priority
- Multiple state machine instances with broadcast support
- Event completion tracking with Done channels
- Tracing support for state transitions
- Event deferral support
- State machine-level activity actions
- Automatic termination with final states

## Core Concepts

A state machine is a computational model that defines how a system behaves and transitions between different states. Here are key concepts:

- **State**: A condition or situation of the system at a specific moment. For example, a traffic light can be in states like "red", "yellow", or "green".
- **Event**: A trigger that can cause the system to change states. Events can be external (user actions) or internal (timeouts).
- **Transition**: A change from one state to another in response to an event.
- **Guard**: A condition that must be true for a transition to occur.
- **Action**: Code that executes when entering/exiting states or during transitions.
- **Hierarchical States**: States that contain other states, allowing for complex behavior modeling with inheritance.
- **Initial State**: The starting state when the machine begins execution.
- **Final State**: A state indicating the machine has completed its purpose.

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
        hsm.Trigger("moveToBar"),
        hsm.Source("foo"),
        hsm.Target("bar")
    ),
    hsm.Initial("foo")
)

// Create and start the state machine
sm := hsm.Start(context.Background(), &MyHSM{}, &model)

// Create event with completion channel
done := make(chan struct{})
event := hsm.Event{
    Name: "moveToBar",
    Done: done,
}

// Dispatch event and wait for completion
sm.Dispatch(event)
<-done
```

### State Actions

States can have multiple types of actions:

```go
type MyHSM struct {
    hsm.HSM
    status string
}

hsm.State("active",
    // Entry action - runs once when state is entered
    hsm.Entry(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        log.Println("Entering active state")
    }),

    // Multiple activity actions - long-running operations with context
    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        for {
            select {
            case <-ctx.Done():
                return
            case <-time.After(time.Second):
                log.Println("Activity 1 tick")
            }
        }
    }),
    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        for {
            select {
            case <-ctx.Done():
                return
            case <-time.After(2 * time.Second):
                log.Println("Activity 2 tick")
            }
        }
    }),

    // Exit action - runs when leaving the state
    hsm.Exit(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        log.Println("Exiting active state")
    })
)
```

### State Machine Actions

The state machine itself can have activity actions:

```go
model := hsm.Define(
    "example",
    // Activity action for the entire state machine
    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        for {
            select {
            case <-ctx.Done():
                return
            case <-time.After(time.Second):
                log.Println("State machine background activity")
            }
        }
    }),

    // States and transitions...
)
```

### Logging Support

The state machine supports structured logging through a Logger interface:

```go
// Define a logger implementation
type MyLogger struct {}

func (l *MyLogger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
    // Implement logging logic
}

// Use the logger in state machine configuration
sm := hsm.Start(ctx, &MyHSM{}, &model, hsm.Config{
    Logger: &MyLogger{},
    Id:    "my-hsm",
    Name:  "MyHSM",
})

// Use logging in state actions
hsm.State("active",
    hsm.Entry(hsm.Log[*MyHSM](slog.LevelInfo, "Entering active state")),
    hsm.Exit(hsm.Log[*MyHSM](slog.LevelInfo, "Exiting active state"))
)
```

### State Machine Lifecycle Management

Additional lifecycle management features:

```go
// Restart a state machine
sm.Restart(context.Background())  // Returns to initial state

// Stop a state machine
done := sm.Stop(context.Background())
<-done  // Wait for completion

// Get current queue length
queueLen := sm.QueueLen()

// Get state machine context
ctx := sm.Context()
```

### Event Dispatch Methods

Multiple ways to dispatch events:

```go
// Direct dispatch to a specific state machine
done := sm.Dispatch(ctx, hsm.Event{Name: "myEvent"})
<-done  // Wait for completion

// Dispatch through context
done = hsm.Dispatch(ctx, hsm.Event{Name: "myEvent"})
<-done

// Broadcast to all state machines
done = hsm.DispatchAll(ctx, hsm.Event{Name: "globalEvent"})
<-done

// Dispatch to specific state machine by ID
done = hsm.DispatchTo(ctx, "machine-1", hsm.Event{Name: "targetedEvent"})
<-done
```

### Pattern Matching

Support for wildcard pattern matching in state and event names:

```go
// Match state patterns
matched := hsm.Match("/state/substate", "/state/*")  // true
matched = hsm.Match("/foo/bar/baz", "/foo/bar")     // false

// Use wildcards in event triggers
hsm.Transition(
    hsm.Trigger("*.event.*"),  // Matches any event with middle segment "event"
    hsm.Source("active"),
    hsm.Target("next")
)
```

### Event Deferral

States can defer events to be processed later:

```go
hsm.State("busy",
    // Defer "update" events until we leave this state
    hsm.Defer("update"),

    hsm.Transition(
        hsm.Trigger("complete"),
        hsm.Target("idle")
        // When transitioning to idle, deferred "update" events will be processed
    )
)
```

### Final States

A top-level final state will automatically terminate the state machine:

```go
model := hsm.Define(
    "example",
    hsm.State("active"),
    hsm.State("final", hsm.Final()),  // This is a final state
    hsm.Transition(
        hsm.Trigger("complete"),
        hsm.Source("active"),
        hsm.Target("final")           // Transitioning here will terminate the state machine
    ),
    hsm.Initial("active")
)
```

### Choice States

Choice pseudo-states allow dynamic branching based on conditions:

```go
type MyHSM struct {
    hsm.HSM
    score int
}

hsm.State("processing",
    hsm.Transition(
        hsm.Trigger("decide"),
        hsm.Target(
            hsm.Choice(
                // First matching guard wins
                hsm.Transition(
                    hsm.Target("approved"),
                    hsm.Guard(func(ctx context.Context, hsm *MyHSM, event hsm.Event) bool {
                        return hsm.score > 700
                    }),
                ),
                // Default transition (no guard)
                hsm.Transition(
                    hsm.Target("rejected")
                ),
            ),
        ),
    ),
)
```

### Event Broadcasting

Multiple state machine instances can receive broadcasted events:

```go
type MyHSM struct {
    hsm.HSM
    id string
}

sm1 := hsm.Start(context.Background(), &MyHSM{id: "sm1"}, &model)
sm2 := hsm.Start(context.Background(), &MyHSM{id: "sm2"}, &model)

// Dispatch event to all state machines
hsm.DispatchAll(sm1, hsm.NewEvent("globalEvent"))
```

### Transitions

Transitions define how states change in response to events:

```go
type MyHSM struct {
    hsm.HSM
    data []string
}

hsm.Transition(
    hsm.Trigger("submit"),
    hsm.Source("draft"),
    hsm.Target("review"),
    hsm.Guard(func(ctx context.Context, hsm *MyHSM, event hsm.Event) bool {
        return len(hsm.data) > 0
    }),
    hsm.Effect(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        log.Println("Transitioning from draft to review")
    })
)
```

### Hierarchical States

States can be nested to create hierarchical state machines:

```go
type MachineHSM struct {
    hsm.HSM
    status string
}

model := hsm.Model(
    hsm.State("operational",
        hsm.State("idle"),
        hsm.State("running"),
        hsm.Initial("idle"),
        hsm.Transition(
            hsm.Trigger("start"),
            hsm.Source("idle"),
            hsm.Target("running")
        )
    ),
    hsm.State("maintenance"),
    hsm.Initial("operational")
)
```

### Time-Based Transitions

Create transitions that occur after a time delay or at regular intervals:

```go
type TimerHSM struct {
    hsm.HSM
    timeout time.Duration
}

// One-time delayed transition
hsm.Transition(
    hsm.After(func(ctx context.Context, hsm *TimerHSM) time.Duration {
        return hsm.timeout
    }),
    hsm.Source("active"),
    hsm.Target("timeout")
)

// Recurring transition every interval
hsm.Transition(
    hsm.Every(func(ctx context.Context, hsm *TimerHSM) time.Duration {
        return time.Second * 5  // Triggers every 5 seconds
    }),
    hsm.Source("active"),
    hsm.Effect(func(ctx context.Context, hsm *TimerHSM, event hsm.Event) {
        log.Println("Recurring action")
    })
)
```

### Context Usage in Activities

Activities receive a context that is cancelled when the state is exited. For operations that need to live beyond the state's lifetime, use the state machine's context instead:

```go
type MyHSM struct {
    hsm.HSM
    data chan string
}

hsm.State("processing",
    // Activity bound to state lifetime
    hsm.Activity(func(ctx context.Context, hsm *MyHSM, event hsm.Event) {
        // This goroutine will be cancelled when leaving the state
        for {
            select {
            case <-ctx.Done():
                return
            case data := <-hsm.data:
                log.Println("Processed:", data)
            }
        }
    }),

    // Activity using state machine context
    hsm.Activity(func(stateCtx context.Context, hsm *MyHSM, event hsm.Event) {
        // Use sm.Context() for operations that should continue across state changes
        smCtx := hsm.Context()
        go func() {
            for {
                select {
                case <-smCtx.Done():
                    return
                case data := <-hsm.data:
                    log.Println("Long-running process:", data)
                }
            }
        }()
    })
)
```

Note: Be careful when using the state machine's context in activities, as these operations will continue running until the state machine is stopped, regardless of state changes.

### Event Completion Tracking

Track event completion using Done channels:

```go
type ProcessHSM struct {
    hsm.HSM
    result string
}

// Create event with completion channel
done := make(chan struct{})
event := hsm.Event{
    Name: "process",
    Data: payload,
    Done: done,
}

// Dispatch event
sm.Dispatch(event)

// Wait for processing to complete
select {
case <-done:
    log.Println("Event processing completed")
case <-time.After(time.Second):
    log.Println("Timeout waiting for event processing")
}
```

### Tracing Support

Enable tracing for debugging state transitions:

```go
type TracedHSM struct {
    hsm.HSM
    id string
}

// Create tracer
trace := func(ctx context.Context, step string, data ...any) (context.Context, func(...any)) {
    log.Printf("[TRACE] %s: %+v", step, data)
    return ctx, func(...any) {}
}

// Start state machine with tracing
sm := hsm.Start(ctx, &TracedHSM{id: "machine-1"}, &model, hsm.Config{
    Trace: trace,
    Id:    "machine-1",
})
```

### OpenTelemetry Integration

The package's `Trace` interface can be used to integrate with OpenTelemetry:

```go
type TelemetryHSM struct {
    hsm.HSM
    serviceName string
}

// Example implementation of hsm.Trace interface using OpenTelemetry
func NewOTelTracer(name string) hsm.Trace {
    provider := initTracerProvider(name)
    tracer := provider.Tracer(name)

    return func(ctx context.Context, step string, data ...any) (context.Context, func(...any)) {
        attrs := []attribute.KeyValue{
            attribute.String("step", step),
        }

        ctx, span := tracer.Start(ctx, step, trace.WithAttributes(attrs...))
        return ctx, func(...any) {
            span.End()
        }
    }
}

// Usage with state machine
sm := hsm.Start(ctx, &TelemetryHSM{serviceName: "payment"}, &model, hsm.Config{
    Trace: NewOTelTracer("payment_processor"),
    Id:    "payment-1",
})
```

## Roadmap

Current and planned features:

- [x] Event-driven transitions
- [x] Time-based transitions with delays
- [x] Hierarchical state nesting
- [x] Entry/exit/activity actions
- [x] Guard conditions
- [x] Transition effects
- [x] Choice pseudo-states
- [x] Event broadcasting
- [x] Concurrent activities
- [ ] Scheduled transitions (at specific dates/times)
  ```go
  hsm.Transition(
      hsm.At(time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)),
      hsm.Source("active"),
      hsm.Target("expired")
  )
  ```
- [ ] History support (shallow and deep)
  ```go
  hsm.State("parent",
      hsm.History(), // Shallow history
      hsm.DeepHistory(), // Deep history
      hsm.State("child1"),
      hsm.State("child2")
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
- Signature changes follow the new context+event pattern
