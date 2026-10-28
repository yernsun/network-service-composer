package nodecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	eventtypes "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/api/types/system"
	"github.com/yernsun/network-service-composer/internal/runtime/engine"
)

type timelineMonitor struct {
	engine.MonitoringClient
	messages chan eventtypes.Message
	failures chan error
}

func (m *timelineMonitor) Info(context.Context) (system.Info, error) {
	return system.Info{ID: "engine-a", Swarm: swarm.Info{NodeID: "node-a"}}, nil
}
func (m *timelineMonitor) Events(context.Context, []string) (<-chan eventtypes.Message, <-chan error) {
	return m.messages, m.failures
}
func (m *timelineMonitor) Close() error { return nil }

type timelineRoundTrip func(*http.Request) (*http.Response, error)

func (fn timelineRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func timelineResponse(status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
}

func timelineDockerMessage(id string) eventtypes.Message {
	return eventtypes.Message{Type: eventtypes.ContainerEventType, Action: eventtypes.Action("die"),
		Actor: eventtypes.Actor{ID: id, Attributes: map[string]string{
			"com.docker.compose.project": "demoapp", "com.docker.compose.service": "api", "sensitive": "secret-value",
		}}, TimeNano: time.Now().UTC().UnixNano()}
}

func TestCollectorRetriesUnacknowledgedEventsWithoutRawDockerAttributes(t *testing.T) {
	monitor := &timelineMonitor{messages: make(chan eventtypes.Message, 1), failures: make(chan error)}
	monitor.messages <- timelineDockerMessage("container-a")
	var mu sync.Mutex
	batches := []EventBatch{}
	attempts := 0
	acknowledged := make(chan struct{}, 1)
	client := &http.Client{Transport: timelineRoundTrip(func(request *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
		}
		if bytes.Contains(raw, []byte("secret-value")) {
			t.Error("raw Docker attribute crossed the node boundary")
		}
		var batch EventBatch
		if err := json.Unmarshal(raw, &batch); err != nil {
			t.Error(err)
		}
		mu.Lock()
		batches = append(batches, batch)
		if len(batch.Events) > 0 {
			attempts++
			if attempts == 1 {
				mu.Unlock()
				return timelineResponse(http.StatusServiceUnavailable), nil
			}
			select {
			case acknowledged <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
		return timelineResponse(http.StatusNoContent), nil
	})}
	ctx, cancel := context.WithCancel(t.Context())
	collector := Collector{Binding: Binding{NodeID: "node-a", EngineID: "engine-a", URL: "https://controller.test"},
		Interval: 5 * time.Millisecond, Open: func() (engine.MonitoringClient, error) { return monitor, nil }}
	stopped := make(chan struct{})
	go func() { defer close(stopped); collector.collectEvents(ctx, client, Session{Generation: 1}) }()
	select {
	case <-acknowledged:
	case <-time.After(3 * time.Second):
		t.Fatal("unacknowledged event was not retried")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(batches) < 3 || batches[0].State != "stream_disconnected" || attempts != 2 {
		t.Fatalf("collector acknowledgment sequence: %#v attempts=%d", batches, attempts)
	}
	var deliveries []EventBatch
	for _, batch := range batches {
		if len(batch.Events) > 0 {
			deliveries = append(deliveries, batch)
		}
	}
	if len(deliveries) != 2 || deliveries[0].Events[0].ExternalID != "container-a" ||
		deliveries[0].Events[0] != deliveries[1].Events[0] || deliveries[0].Generation != 1 {
		t.Fatalf("retry changed event identity: %#v", deliveries)
	}
}

func TestCollectorReportsOverflowWhenUnacknowledgedBufferFills(t *testing.T) {
	monitor := &timelineMonitor{messages: make(chan eventtypes.Message, 201), failures: make(chan error)}
	for index := 0; index < 201; index++ {
		monitor.messages <- timelineDockerMessage("container-a")
	}
	overflow := make(chan struct{}, 1)
	client := &http.Client{Transport: timelineRoundTrip(func(request *http.Request) (*http.Response, error) {
		var batch EventBatch
		if err := json.NewDecoder(request.Body).Decode(&batch); err != nil {
			t.Error(err)
		}
		if batch.State == "overflow" {
			select {
			case overflow <- struct{}{}:
			default:
			}
		}
		if len(batch.Events) > 0 {
			return timelineResponse(http.StatusServiceUnavailable), nil
		}
		return timelineResponse(http.StatusNoContent), nil
	})}
	ctx, cancel := context.WithCancel(t.Context())
	collector := Collector{Binding: Binding{NodeID: "node-a", EngineID: "engine-a", URL: "https://controller.test"},
		Interval: 5 * time.Millisecond, Open: func() (engine.MonitoringClient, error) { return monitor, nil }}
	stopped := make(chan struct{})
	go func() { defer close(stopped); collector.collectEvents(ctx, client, Session{Generation: 1}) }()
	select {
	case <-overflow:
	case <-time.After(3 * time.Second):
		t.Fatal("unacknowledged overflow was not reported")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not stop")
	}
}
