package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yernsun/network-service-composer/internal/core"
)

func TestServiceLogsAdminAndSwarmBoundaries(t *testing.T) {
	s, _, _, _, route, _ := resourcePatchFixture(t)
	router := chi.NewRouter()
	router.Get("/services/{id}/logs", s.serviceLogs)
	for _, test := range []struct {
		role   string
		status int
	}{{"viewer", http.StatusForbidden}, {"administrator", http.StatusUnprocessableEntity}} {
		request := httptest.NewRequest(http.MethodGet, "/services/"+route.ServiceID+"/logs", nil)
		request = request.WithContext(context.WithValue(request.Context(), identityContextKey{}, core.User{ID: "user", Role: test.role, Enabled: true}))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("%s: got %d %s", test.role, response.Code, response.Body.String())
		}
	}
}

func TestServiceLogWriterBoundsFramesAndSeparatesStreams(t *testing.T) {
	lines := make(chan serviceLogLine, 4)
	writer := &serviceLogWriter{stream: "stderr", lines: lines, ctx: context.Background()}
	if _, err := writer.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatal(err)
	}
	if first, second := <-lines, <-lines; first.Stream != "stderr" || first.Text != "one" || second.Text != "two" {
		t.Fatalf("unexpected lines: %+v %+v", first, second)
	}
	if _, err := writer.Write(bytes.Repeat([]byte{'x'}, maxServiceLogLine+1)); err == nil {
		t.Fatal("oversized frame accepted")
	}
	writer = &serviceLogWriter{stream: "stdout", lines: lines, ctx: context.Background()}
	if _, err := writer.Write([]byte(strings.Repeat("x", maxServiceLogLine+1) + "\n")); err == nil {
		t.Fatal("oversized terminated frame accepted")
	}
}

func TestServiceLogWriterStopsWhenClientDisconnects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	writer := &serviceLogWriter{stream: "stdout", lines: make(chan serviceLogLine), ctx: ctx}
	if _, err := writer.Write([]byte("line\n")); err == nil {
		t.Fatal("cancelled log stream continued writing")
	}
}

func TestInternalServiceLogsRequiresMutualTLS(t *testing.T) {
	s, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/service-logs", nil)
	response := httptest.NewRecorder()
	s.InternalServiceLogs().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated node log stream returned %d", response.Code)
	}
}

type recordingLogResponse struct {
	mu      sync.Mutex
	headers http.Header
	body    bytes.Buffer
}

func (w *recordingLogResponse) Header() http.Header { return w.headers }
func (w *recordingLogResponse) WriteHeader(int)     {}
func (w *recordingLogResponse) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *recordingLogResponse) Flush() {}
func (w *recordingLogResponse) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

type trackedLogReader struct {
	io.ReadCloser
	closed chan struct{}
}

func (r *trackedLogReader) Close() error { err := r.ReadCloser.Close(); close(r.closed); return err }

func TestServiceLogSSEFollowsBothStreamsAndClosesOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	tracked := &trackedLogReader{ReadCloser: reader, closed: make(chan struct{})}
	response := &recordingLogResponse{headers: make(http.Header)}
	finished := make(chan struct{})
	go func() { serveServiceLogStream(ctx, response, serviceLogStream{ReadCloser: tracked}); close(finished) }()
	for _, frame := range []struct {
		stream byte
		line   string
	}{{1, "first\n"}, {2, "warning\n"}} {
		header := make([]byte, 8)
		header[0] = frame.stream
		binary.BigEndian.PutUint32(header[4:], uint32(len(frame.line)))
		if _, err := writer.Write(append(header, frame.line...)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.After(2 * time.Second)
	for !strings.Contains(response.String(), `"text":"warning"`) {
		select {
		case <-deadline:
			t.Fatalf("missing streamed stderr frame: %s", response.String())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if !strings.Contains(response.String(), `"stream":"stdout"`) || !strings.Contains(response.String(), `"stream":"stderr"`) {
		t.Fatalf("streams not demultiplexed: %s", response.String())
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE did not stop on cancellation")
	}
	select {
	case <-tracked.closed:
	default:
		t.Fatal("Docker log reader was not closed")
	}
	_ = writer.Close()
}
