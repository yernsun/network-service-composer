package gateway

import (
	"strconv"
	"testing"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

const (
	largeSnapshotServices = 10_000
	largeSnapshotDomains  = 20_000
)

// BenchmarkEncodeLargeSnapshot exercises the v2 DNS-only capacity target without making
// every unit-test run allocate the full synthetic data set. Run it with:
//
//	go test -run '^$' -bench '^BenchmarkEncodeLargeSnapshot$' -benchtime=3x ./internal/gateway
func BenchmarkEncodeLargeSnapshot(b *testing.B) {
	snapshot := largeSyntheticSnapshot()
	encoded, err := EncodeSnapshot(snapshot, DefaultChunkSize)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err = EncodeSnapshot(snapshot, DefaultChunkSize); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(encoded.Header.UncompressedSize), "raw-bytes")
	b.ReportMetric(float64(encoded.Header.CompressedSize), "gzip-bytes")
}

func largeSyntheticSnapshot() model.Snapshot {
	snapshot := model.Snapshot{
		PoolID:       "pool_benchmark",
		Revision:     42,
		GeneratedAt:  time.Unix(1_700_000_000, 0).UTC(),
		Services:     make(map[string]model.SnapshotService, largeSnapshotServices),
		Certificates: map[string]model.SnapshotCertificate{},
		Domains:      make([]model.SnapshotDomain, 0, largeSnapshotDomains),
		Routes:       make([]model.SnapshotRoute, 0, largeSnapshotDomains),
	}
	for serviceIndex := range largeSnapshotServices {
		serviceID := "svc_" + strconv.Itoa(serviceIndex)
		qualifiedName := "benchmark_service_" + strconv.Itoa(serviceIndex)
		snapshot.Services[serviceID] = model.SnapshotService{ID: serviceID, QualifiedName: qualifiedName, DNSName: qualifiedName, Protocol: "http"}
	}
	for domainIndex := range largeSnapshotDomains {
		host := "app-" + strconv.Itoa(domainIndex) + ".example.test"
		domainID := "dom_" + strconv.Itoa(domainIndex)
		snapshot.Domains = append(snapshot.Domains, model.SnapshotDomain{ID: domainID, Host: host, TLSMode: model.TLSDisabled, HTTPEnabled: true})
		snapshot.Routes = append(snapshot.Routes, model.SnapshotRoute{
			ID:               "route_" + strconv.Itoa(domainIndex),
			Host:             host,
			Path:             "/",
			ServiceID:        "svc_" + strconv.Itoa(domainIndex%largeSnapshotServices),
			TargetPort:       8_000,
			TLSMode:          model.TLSDisabled,
			UpstreamProtocol: "http",
		})
	}
	return snapshot
}
