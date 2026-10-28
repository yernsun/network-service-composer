package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/model"
)

func TestSnapshotEncodingIsDeterministic(t *testing.T) {
	value := model.Snapshot{PoolID: "pool_test", Revision: 7, GeneratedAt: time.Unix(1, 2).UTC(),
		Services: map[string]model.SnapshotService{
			"svc_b": {ID: "svc_b", QualifiedName: "demo_b", DNSName: "demo_b"},
			"svc_a": {ID: "svc_a", QualifiedName: "demo_a", DNSName: "demo_a"},
		},
		Certificates: map[string]model.SnapshotCertificate{"cert_a": {ID: "cert_a", Domains: []string{"example.com"}, CertificatePEM: "cert", PrivateKeyPEM: "key"}},
	}
	first, err := EncodeSnapshot(value, 9)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeSnapshot(value, 9)
	if err != nil {
		t.Fatal(err)
	}
	if first.Header.ModelDigest != second.Header.ModelDigest || first.Header.TransportDigest != second.Header.TransportDigest {
		t.Fatalf("non-deterministic digest: %#v != %#v", first.Header, second.Header)
	}
	decoded, err := DecodeSnapshot(first.Header, first.Chunks)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.PoolId != value.PoolID || decoded.ConfigRevision != value.Revision || len(decoded.Services) != 2 {
		t.Fatalf("unexpected decoded snapshot: %#v", decoded)
	}
}

func FuzzDecodeSnapshot(f *testing.F) {
	f.Add([]byte("not-gzip"), int64(10))
	f.Add([]byte{0x1f, 0x8b, 0x08, 0x00}, int64(0))
	f.Fuzz(func(t *testing.T, data []byte, uncompressedSize int64) {
		if len(data) > 2<<20 {
			t.Skip()
		}
		if uncompressedSize < 0 {
			uncompressedSize = -(uncompressedSize + 1)
		}
		uncompressedSize %= 2 << 20
		digest := sha256.Sum256(data)
		header := &gatewayv2.SnapshotHeader{ConfigRevision: 1, ModelDigest: strings.Repeat("0", 64), TransportDigest: hex.EncodeToString(digest[:]), CompressedSize: int64(len(data)), UncompressedSize: uncompressedSize, ChunkCount: 1}
		_, _ = DecodeSnapshot(header, []*gatewayv2.SnapshotChunk{{ConfigRevision: 1, Index: 0, Data: data}})
	})
}

func TestSnapshotRejectsCorruption(t *testing.T) {
	value := model.Snapshot{PoolID: "pool_test", Revision: 1, Services: map[string]model.SnapshotService{}, Certificates: map[string]model.SnapshotCertificate{}}
	encoded, err := EncodeSnapshot(value, 1024)
	if err != nil {
		t.Fatal(err)
	}
	encoded.Chunks[0].Data[0] ^= 0xff
	if _, err = DecodeSnapshot(encoded.Header, encoded.Chunks); err == nil {
		t.Fatal("expected corrupted snapshot to be rejected")
	}
}

func TestIPAccessControlSnapshotRoundTrip(t *testing.T) {
	snapshot := model.Snapshot{PoolID: "pool_acl", Revision: 1, AccessControls: []model.IPAccessControl{{Scope: "server", ResourceID: "dom_acl", Access: []string{" 192.168.1.10-192.168.1.20 "}, Forbidden: []string{"2001:db8::/32"}, ClientIP: &model.ClientIPSource{Header: "X-Forwarded-For", TrustedProxies: []string{" 10.0.0.1-10.0.0.9 "}}}}}
	encoded, err := EncodeSnapshot(snapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSnapshot(encoded.Header, encoded.Chunks)
	if err != nil || len(decoded.AccessControls) != 1 || decoded.AccessControls[0].Access[0] != snapshot.AccessControls[0].Access[0] {
		t.Fatal(decoded, err)
	}
	if source := decoded.AccessControls[0].ClientIp; source == nil || source.Header != "X-Forwarded-For" || source.TrustedProxies[0] != " 10.0.0.1-10.0.0.9 " {
		t.Fatal(source)
	}
	snapshot.AccessControls[0].Forbidden[0] = "::1"
	changed, err := EncodeSnapshot(snapshot, 0)
	if err != nil || changed.Header.ModelDigest == encoded.Header.ModelDigest {
		t.Fatal("ACL omitted from model digest", err)
	}
	snapshot.AccessControls[0].ClientIP.Header = "X-Client-IP"
	sourceChanged, err := EncodeSnapshot(snapshot, 0)
	if err != nil || sourceChanged.Header.ModelDigest == changed.Header.ModelDigest {
		t.Fatal("client source omitted from model digest", err)
	}
	snapshot.AccessControls[0].ClientIP.TrustedProxies = []string{"::1"}
	trustChanged, err := EncodeSnapshot(snapshot, 0)
	if err != nil || trustChanged.Header.ModelDigest == sourceChanged.Header.ModelDigest {
		t.Fatal("trusted proxies omitted from model digest", err)
	}
}
