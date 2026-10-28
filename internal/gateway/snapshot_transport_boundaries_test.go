package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type snapshotTransportStore struct {
	storage.Store
	stage    string
	failure  error
	recorded bool
	built    int
}

func (s *snapshotTransportStore) BuildSnapshot(ctx context.Context, pool string) (model.Snapshot, error) {
	s.built++
	if s.stage == "build" {
		return model.Snapshot{}, s.failure
	}
	v, e := s.Store.BuildSnapshot(ctx, pool)
	if s.stage == "encode" {
		v.ChallengeUpstream = string([]byte{255})
	}
	if s.stage == "marshal" {
		v.GeneratedAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return v, e
}
func (s *snapshotTransportStore) GetControlDocument(ctx context.Context, key string) (storage.ControlDocument, error) {
	if strings.HasSuffix(key, ":nginx-release-head") && (s.stage == "claim" || s.stage == "mark sent" && s.recorded) {
		return storage.ControlDocument{}, s.failure
	}
	return s.Store.GetControlDocument(ctx, key)
}
func (s *snapshotTransportStore) RecordSnapshot(ctx context.Context, pool string, revision int64, digest string, payload []byte, now time.Time) error {
	if s.stage == "record" {
		return s.failure
	}
	e := s.Store.RecordSnapshot(ctx, pool, revision, digest, payload, now)
	if e == nil {
		s.recorded = true
	}
	return e
}

type snapshotTransportStream struct {
	*boundaryGatewayStream
	stage   string
	failure error
}

func (s *snapshotTransportStream) Send(m *gatewayv2.ControllerMessage) error {
	if s.stage == "chunk" && m.GetSnapshotChunk() != nil {
		return s.failure
	}
	return s.boundaryGatewayStream.Send(m)
}

func TestSnapshotTransportFailuresNeverConfirmUnloadedConfiguration(t *testing.T) {
	for _, stage := range []string{"claim", "build", "encode", "marshal", "record", "mark sent", "chunk", "managed", "managed held", "managed transport failure", "managed completed", "managed completed ACK", "managed completed transport failure"} {
		t.Run(stage, func(t *testing.T) {
			server, base, stream, hello := gatewayHandshakeFixture(t)
			failure := errors.New("private snapshot transport failure")
			store := &snapshotTransportStore{Store: base, stage: stage, failure: failure}
			cfg := config.Defaults()
			ring, e := security.NewKeyring("fixture", map[string]string{"fixture": base64.StdEncoding.EncodeToString(make([]byte, 32))})
			if e != nil {
				t.Fatal(e)
			}
			server.store = store
			server.core = core.New(store, cfg, ring)
			managed := strings.HasPrefix(stage, "managed")
			completed := strings.HasPrefix(stage, "managed completed")
			var history storage.ControlDocument
			var ack *gatewayv2.ApplyAck
			if managed {
				target := hello.GatewayId
				if stage == "managed held" {
					target = "other-gateway"
				}
				release := core.NginxRelease{PoolID: hello.PoolId, ConfigRevision: 2, Status: "PUBLISHING", Targets: []core.NginxReleaseTarget{{GatewayID: target, Status: "PENDING"}}}
				if completed {
					now := time.Now().Add(-time.Hour)
					release.Status, release.FinishedAt = "SUCCEEDED", &now
					release.Targets[0].Status = "SUCCEEDED"
					release.Targets[0].ConfirmedRevision = 2
					release.Targets[0].RenderedDigest = strings.Repeat("b", 64)
				}
				gatewayControlFixture(t, base, "release", "nginx-release", hello.PoolId, release)
				history, e = base.GetControlDocument(context.Background(), "release")
				if e != nil {
					t.Fatal(e)
				}
				gatewayControlFixture(t, base, hello.PoolId+":nginx-release-head", "nginx-release-head", hello.PoolId, map[string]string{"release_id": "release"})
				snapshot := model.Snapshot{PoolID: hello.PoolId, Revision: 2, GeneratedAt: time.Unix(0, 0), Services: map[string]model.SnapshotService{}, Certificates: map[string]model.SnapshotCertificate{"certificate": {ID: "certificate", CertificatePEM: "public fixture"}}}
				if stage == "managed completed ACK" {
					certificate := snapshot.Certificates["certificate"]
					certificate.PrivateKeyPEM = "private fixture"
					snapshot.Certificates["certificate"] = certificate
					encoded, err := EncodeSnapshot(snapshot, DefaultChunkSize)
					if err != nil {
						t.Fatal(err)
					}
					ack = &gatewayv2.ApplyAck{ConfigRevision: 2, ModelDigest: encoded.Header.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("c", 64)}
					certificate.PrivateKeyPEM = ""
					snapshot.Certificates["certificate"] = certificate
				}
				raw, e := json.Marshal(map[string]any{"snapshot": snapshot, "keys": map[string]string{"certificate": "private fixture"}})
				if e != nil {
					t.Fatal(e)
				}
				cipher, e := ring.Encrypt(raw, []byte("control:release:snapshot"))
				if e != nil {
					t.Fatal(e)
				}
				gatewayControlFixture(t, base, "release:snapshot", "release-snapshot", hello.PoolId, map[string]any{"ciphertext": cipher, "key_id": "fixture"})
			}
			stream.input = []*gatewayv2.AgentMessage{{Payload: &gatewayv2.AgentMessage_Hello{Hello: hello}}}
			if ack != nil {
				stream.input = append(stream.input, &gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: ack}})
			}
			transport := &snapshotTransportStream{boundaryGatewayStream: stream, stage: stage, failure: failure}
			if stage == "managed transport failure" || stage == "managed completed transport failure" {
				transport.stage = "chunk"
			}
			e = server.Connect(transport)
			success := stage == "managed" || stage == "managed held" || stage == "managed completed" || stage == "managed completed ACK"
			if success {
				if e != nil {
					t.Fatal(e)
				}
			} else if status.Code(e) != codes.Internal || strings.Contains(e.Error(), "private snapshot") {
				t.Fatal("transport failure exposed secrets or claimed success", e)
			}
			g, e := base.GetGateway(context.Background(), hello.GatewayId)
			if ack != nil {
				if e != nil || g.AppliedRevision != 2 || g.ModelDigest != ack.ModelDigest || g.RendererVersion != ack.RendererVersion || g.RenderedDigest != ack.RenderedDigest {
					t.Fatal("matching recovery ACK did not restore current Gateway evidence", g, e)
				}
			} else if e != nil || g.AppliedRevision != 0 || g.ModelDigest != "" {
				t.Fatal("undelivered/unacknowledged snapshot marked applied", g, e)
			}
			headers, chunks := 0, 0
			var header *gatewayv2.SnapshotHeader
			var payload []*gatewayv2.SnapshotChunk
			for _, message := range stream.output {
				if message.GetSnapshotHeader() != nil {
					headers++
					header = message.GetSnapshotHeader()
				}
				if message.GetSnapshotChunk() != nil {
					chunks++
					payload = append(payload, message.GetSnapshotChunk())
				}
			}
			if stage == "managed" || stage == "managed completed" || stage == "managed completed ACK" {
				if headers != 1 || chunks < 1 || store.built != 0 {
					t.Fatal("managed release did not use its frozen snapshot", headers, chunks, store.built)
				}
				snapshot, err := DecodeSnapshot(header, payload)
				if err != nil || len(snapshot.Certificates) != 1 || snapshot.Certificates[0].PrivateKeyPem != "private fixture" {
					t.Fatal("frozen TLS key did not survive resynchronization", err)
				}
			} else if stage == "managed held" {
				if headers != 0 || chunks != 0 || store.built != 0 {
					t.Fatal("held release bypassed serial target selection")
				}
			} else if stage != "chunk" && stage != "managed transport failure" && stage != "managed completed transport failure" && headers != 0 {
				t.Fatal("invalid snapshot escaped before validation", stage)
			}
			if completed {
				after, err := base.GetControlDocument(context.Background(), "release")
				if err != nil || after.Revision != history.Revision || !bytes.Equal(after.Payload, history.Payload) {
					t.Fatal("recovery or connection loss rewrote completed release evidence", err)
				}
			} else if managed && stage != "managed held" {
				r, e := server.core.GetNginxRelease(context.Background(), hello.PoolId, "release")
				if e != nil || r.Targets[0].Status != "UNKNOWN" || r.Status != "UNKNOWN" {
					t.Fatal("connection loss hid uncertain execution", r, e)
				}
			}
		})
	}
}
