package queue

import (
	"context"

	"github.com/runpod/hsm/elements"
)

var NonBlocking = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}()

type Queue struct {
	empty  chan bool
	events chan []elements.Event
}

func Make() Queue {
	empty := make(chan bool, 1)
	empty <- true
	return Queue{
		empty:  empty,
		events: make(chan []elements.Event, 1),
	}
}

func (q *Queue) Push(event elements.Event) {
	var events []elements.Event
	select {
	case events = <-q.events:
	case <-q.empty:
	}
	events = append(events, event)
	q.events <- events
}

func (q *Queue) Pop(ctx context.Context) (elements.Event, error) {
	var events []elements.Event
	select {
	case events = <-q.events:
	case <-ctx.Done():
		return elements.Event{}, ctx.Err()
	}
	event := events[0]
	if len(events) == 1 {
		q.empty <- true
	} else {
		q.events <- events[1:]
	}
	return event, nil
}
