package gateway

import (
	"google.golang.org/grpc"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	metricsx "github.com/yernsun/network-service-composer/internal/metrics"
)

func TestValidateAppliedTuple(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, test := range []struct {
		name     string
		revision int64
		model    string
		rendered string
		valid    bool
	}{
		{name: "bootstrap", valid: true},
		{name: "applied", revision: 1, model: digest, rendered: digest, valid: true},
		{name: "bootstrap with digest", model: digest},
		{name: "missing rendered", revision: 1, model: digest},
		{name: "uppercase", revision: 1, model: strings.ToUpper(digest), rendered: digest},
		{name: "non hexadecimal", revision: 1, model: strings.Repeat("z", 64), rendered: digest},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateAppliedTuple(test.revision, test.model, test.rendered)
			if (err == nil) != test.valid {
				t.Fatalf("validateAppliedTuple() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestRecordAgentApplyMetricsByFailedPhase(t *testing.T) {
	metrics := metricsx.New()
	recordAgentApplyMetrics(metrics, "", int64(10*time.Millisecond), int64(20*time.Millisecond), true)
	recordAgentApplyMetrics(metrics, "validate", int64(30*time.Millisecond), 0, false)
	recordAgentApplyMetrics(metrics, "reload", int64(40*time.Millisecond), int64(50*time.Millisecond), false)

	assertCounter := func(name string, got, want float64) {
		t.Helper()
		if got != want {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
	assertCounter("validation success", testutil.ToFloat64(metrics.NGINXValidations.WithLabelValues("success")), 2)
	assertCounter("validation failure", testutil.ToFloat64(metrics.NGINXValidations.WithLabelValues("failure")), 1)
	assertCounter("reload success", testutil.ToFloat64(metrics.NGINXReloads.WithLabelValues("success")), 1)
	assertCounter("reload failure", testutil.ToFloat64(metrics.NGINXReloads.WithLabelValues("failure")), 1)
}

func TestRegistersOnlyCurrentGatewayService(t *testing.T) {
	server := grpc.NewServer()
	defer server.Stop()
	(&Server{}).Register(server)
	services := server.GetServiceInfo()
	if len(services) != 1 {
		t.Fatalf("unexpected registered services: %v", services)
	}
	if _, ok := services["nsc.gateway.v2.GatewayControl"]; !ok {
		t.Fatalf("current gateway service missing: %v", services)
	}
}
