package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/core"
	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/model"
	nginxx "github.com/yernsun/network-service-composer/internal/nginx"
	"github.com/yernsun/network-service-composer/internal/pki"
	"github.com/yernsun/network-service-composer/internal/security"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestMTLSGatewayRegistrationSnapshotAndACK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	directory := t.TempDir()
	store, err := storage.Open("sqlite", "file:"+filepath.Join(directory, "nsc.db")+"?_pragma=foreign_keys(1)&_time_format=sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key := []byte("0123456789abcdef0123456789abcdef")
	ring, err := security.NewKeyring("test", map[string]string{"test": base64.StdEncoding.EncodeToString(key)})
	if err != nil {
		t.Fatal(err)
	}
	application := core.New(store, config.Defaults(), ring)
	now := time.Now().UTC().Truncate(time.Microsecond)
	pool := model.GatewayPool{ID: id.New(id.Pool), Name: "mtls-edge", BackendNetwork: "nsc-ingress", ChallengeUpstream: "http://controller:8080", Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreatePool(ctx, &pool); err != nil {
		t.Fatal(err)
	}
	runtimeValue := model.Runtime{ID: id.New(id.Runtime), PoolID: pool.ID, Name: "runtime", Type: model.RuntimeDocker, Endpoint: "unix:///var/run/docker.sock", Enabled: true, Status: model.RuntimeOnline, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRuntime(ctx, &runtimeValue); err != nil {
		t.Fatal(err)
	}
	service := model.Service{ID: id.New(id.Service), RuntimeID: runtimeValue.ID, ServiceKey: "demo/api", ExternalID: "demo/api",
		Name: "api", Namespace: "demo", QualifiedName: "demo_api", DNSName: "demo_api", DNSStatus: model.DNSReady,
		DiscoveredProtocol: "http", DiscoveredPort: 8080, DiscoveredEnabled: true,
		Readiness: model.ReadinessPolicy{Mode: model.ReadinessDocker}, Status: model.ServiceAvailable, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.UpsertService(ctx, &service); err != nil {
		t.Fatal(err)
	}
	endpoint := model.Endpoint{ID: id.New(id.Endpoint), ServiceID: service.ID, ExternalID: "container", Address: "10.0.0.2", Port: 8080, RuntimeState: model.EndpointRunning, HealthState: model.HealthHealthy, ReadyState: model.Ready, ReadinessSource: model.ReadinessDocker, Weight: 1, FirstSeenAt: now, LastSeenAt: now, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.UpsertEndpoint(ctx, &endpoint); err != nil {
		t.Fatal(err)
	}
	const privateMaterial = "TOP-SECRET-PRIVATE-KEY"
	certificate := model.Certificate{ID: id.New(id.Certificate), PoolID: pool.ID, Provider: "custom", Status: model.CertificateActive, PrimaryDomain: "app.example.com", CertificatePEM: "public-certificate", EncryptionKeyID: ring.PrimaryID(), Revision: 1, CreatedAt: now, UpdatedAt: now}
	certificate.EncryptedPrivateKey, err = ring.Encrypt([]byte(privateMaterial), []byte(certificate.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateCertificate(ctx, &certificate); err != nil {
		t.Fatal(err)
	}
	domain := model.Domain{ID: id.New(id.Domain), PoolID: pool.ID, Host: "app.example.com", TLSMode: model.TLSCustom,
		HTTPSRedirect: true, HTTPEnabled: true, HTTPSEnabled: true, Status: model.DomainReady,
		CertificateID: certificate.ID, Source: model.SourceAPI, Enabled: true, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateDomain(ctx, &domain); err != nil {
		t.Fatal(err)
	}
	route := model.Route{ID: id.New(id.Route), DomainID: domain.ID, ServiceID: service.ID, Path: "/", TargetPort: 8080,
		UpstreamProtocol: "http", Enabled: true, Source: model.SourceAPI, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err = store.CreateRoute(ctx, &route); err != nil {
		t.Fatal(err)
	}
	pool, err = store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}

	caCert := filepath.Join(directory, "ca.crt")
	caKey := filepath.Join(directory, "ca.key")
	serverCert := filepath.Join(directory, "controller.crt")
	serverKey := filepath.Join(directory, "controller.key")
	agentCert := filepath.Join(directory, "agent.crt")
	agentKey := filepath.Join(directory, "agent.key")
	if err = pki.InitCA(caCert, caKey, "test CA", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err = pki.Issue(caCert, caKey, serverCert, serverKey, pki.CertificateOptions{CommonName: "controller.test", DNSNames: []string{"controller.test"}, Duration: time.Hour, Server: true}); err != nil {
		t.Fatal(err)
	}
	gatewayID := id.New(id.Gateway)
	identityURI, err := pki.GatewayIdentityURI(pool.ID, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	if err = pki.Issue(caCert, caKey, agentCert, agentKey, pki.CertificateOptions{CommonName: gatewayID, URIs: []*url.URL{identityURI}, Duration: time.Hour, Client: true}); err != nil {
		t.Fatal(err)
	}

	serverTLS := loadServerTLS(t, serverCert, serverKey, caCert)
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	NewServer(application, NewBroker(), ServerOptions{OfflineTimeout: time.Minute}, nil, nil).Register(grpcServer)
	serveDone := make(chan error, 1)
	go func() { serveDone <- grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
	})

	clientTLS := loadClientTLS(t, agentCert, agentKey, caCert)
	connection, err := grpc.NewClient("passthrough:///nsc-gateway-test",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }),
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := gatewayv2.NewGatewayControlClient(connection)
	incompatibleStream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = incompatibleStream.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Hello{Hello: &gatewayv2.Hello{
		GatewayId: gatewayID, PoolId: pool.ID, ProtocolVersion: ProtocolVersion, RendererVersion: "obsolete-renderer"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = incompatibleStream.Recv(); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("renderer mismatch returned %v", err)
	}
	incompatibleGateway, err := store.GetGateway(ctx, gatewayID)
	if err != nil || incompatibleGateway.Status != model.GatewayIncompatible {
		t.Fatalf("renderer mismatch status = %#v, err=%v", incompatibleGateway, err)
	}

	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Hello{Hello: &gatewayv2.Hello{GatewayId: gatewayID, PoolId: pool.ID, Name: "gateway-test", AgentVersion: "test", ProtocolVersion: ProtocolVersion, RendererVersion: nginxx.RendererVersion, AppliedConfigRevision: 0}}}); err != nil {
		t.Fatal(err)
	}
	header, chunks := receiveSnapshot(t, stream)
	decoded, err := DecodeSnapshot(header, chunks)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ConfigRevision != pool.DesiredRevision || len(decoded.Certificates) != 1 || decoded.Certificates[0].PrivateKeyPem != privateMaterial {
		t.Fatalf("unexpected gateway snapshot: %#v", decoded)
	}
	var recorded []byte
	if err = store.DB().QueryRowContext(ctx, `SELECT public_payload FROM pool_snapshots WHERE pool_id=? AND revision=?`, pool.ID, pool.DesiredRevision).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recorded), privateMaterial) || strings.Contains(strings.ToUpper(string(recorded)), "PRIVATE KEY") {
		t.Fatal("snapshot history persisted plaintext private key material")
	}
	if err = stream.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Nack{Nack: &gatewayv2.ApplyNack{ConfigRevision: header.ConfigRevision, ModelDigest: header.ModelDigest, RendererVersion: nginxx.RendererVersion, Error: "synthetic validation failure", FailedPhase: "validate", ObservedAtUnixNano: time.Now().UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	retryHeader, _ := receiveSnapshot(t, stream)
	if retryHeader.ConfigRevision != header.ConfigRevision || retryHeader.ModelDigest != header.ModelDigest {
		t.Fatalf("NACK retry changed immutable snapshot: first=%#v retry=%#v", header, retryHeader)
	}

	// Advance twice while the retry is still in flight. The Controller must not
	// transmit either revision until this one is ACKed, and must then collapse
	// directly to the newest desired revision.
	route.Path = "/v2/"
	route.UpdatedAt = time.Now().UTC()
	if err = store.UpdateRoute(ctx, &route, route.Revision); err != nil {
		t.Fatal(err)
	}
	route.Revision++
	route.Path = "/v3/"
	route.UpdatedAt = route.UpdatedAt.Add(time.Millisecond)
	if err = store.UpdateRoute(ctx, &route, route.Revision); err != nil {
		t.Fatal(err)
	}
	route.Revision++
	latestPool, err := store.GetPool(ctx, pool.ID)
	if err != nil {
		t.Fatal(err)
	}
	nextHeader := make(chan *gatewayv2.SnapshotHeader, 1)
	receiveError := make(chan error, 1)
	go func() {
		for {
			message, receiveErr := stream.Recv()
			if receiveErr != nil {
				receiveError <- receiveErr
				return
			}
			if value := message.GetSnapshotHeader(); value != nil {
				nextHeader <- value
				return
			}
		}
	}()
	select {
	case value := <-nextHeader:
		t.Fatalf("received revision %d before ACKing in-flight revision %d", value.ConfigRevision, retryHeader.ConfigRevision)
	case receiveErr := <-receiveError:
		t.Fatalf("gateway stream failed before ACK: %v", receiveErr)
	case <-time.After(1200 * time.Millisecond):
	}
	if err = stream.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: retryHeader.ConfigRevision, ModelDigest: retryHeader.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("a", 64), AppliedAtUnixNano: time.Now().UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	var latestHeader *gatewayv2.SnapshotHeader
	select {
	case latestHeader = <-nextHeader:
	case receiveErr := <-receiveError:
		t.Fatalf("gateway stream failed after ACK: %v", receiveErr)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for latest desired revision")
	}
	latestChunks := receiveSnapshotChunks(t, stream, latestHeader)
	latestSnapshot, err := DecodeSnapshot(latestHeader, latestChunks)
	if err != nil {
		t.Fatal(err)
	}
	if latestHeader.ConfigRevision != latestPool.DesiredRevision || len(latestSnapshot.Routes) != 1 || latestSnapshot.Routes[0].Path != "/v3/" {
		t.Fatalf("Controller did not collapse to latest desired state: header=%#v snapshot=%#v", latestHeader, latestSnapshot)
	}
	if err = stream.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Ack{Ack: &gatewayv2.ApplyAck{ConfigRevision: latestHeader.ConfigRevision, ModelDigest: latestHeader.ModelDigest, RendererVersion: nginxx.RendererVersion, RenderedDigest: strings.Repeat("b", 64), AppliedAtUnixNano: time.Now().UnixNano()}}}); err != nil {
		t.Fatal(err)
	}
	waitForGatewayACK(t, ctx, store, gatewayID, latestHeader.ConfigRevision)

	gateway, err := store.GetGateway(ctx, gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	gateway.Enabled = false
	gateway.Status = model.GatewayOffline
	gateway.UpdatedAt = time.Now().UTC()
	if err = store.UpdateGateway(ctx, &gateway, gateway.Revision); err != nil {
		t.Fatal(err)
	}
	for {
		_, err = stream.Recv()
		if err == nil {
			continue
		}
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("active disabled gateway stream returned %v", err)
		}
		break
	}
	rejected, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = rejected.Send(&gatewayv2.AgentMessage{Payload: &gatewayv2.AgentMessage_Hello{Hello: &gatewayv2.Hello{GatewayId: gatewayID, PoolId: pool.ID, ProtocolVersion: ProtocolVersion, RendererVersion: nginxx.RendererVersion}}}); err != nil {
		t.Fatal(err)
	}
	_, err = rejected.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("disabled gateway reconnect returned %v", err)
	}

	select {
	case err = <-serveDone:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Fatal(err)
		}
	default:
	}
}

func loadServerTLS(t *testing.T, certPath, keyPath, caPath string) *tls.Config {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := loadCertPool(t, caPath)
	return &tls.Config{Certificates: []tls.Certificate{pair}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS13}
}

func loadClientTLS(t *testing.T, certPath, keyPath, caPath string) *tls.Config {
	t.Helper()
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: loadCertPool(t, caPath), ServerName: "controller.test", MinVersion: tls.VersionTLS13}
}

func loadCertPool(t *testing.T, path string) *x509.CertPool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		t.Fatal("CA contains no certificate")
	}
	return pool
}

func receiveSnapshot(t *testing.T, stream grpc.BidiStreamingClient[gatewayv2.AgentMessage, gatewayv2.ControllerMessage]) (*gatewayv2.SnapshotHeader, []*gatewayv2.SnapshotChunk) {
	t.Helper()
	var header *gatewayv2.SnapshotHeader
	var chunks []*gatewayv2.SnapshotChunk
	for header == nil || int32(len(chunks)) < header.ChunkCount {
		message, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if value := message.GetSnapshotHeader(); value != nil {
			header = value
		}
		if value := message.GetSnapshotChunk(); value != nil {
			chunks = append(chunks, value)
		}
	}
	return header, chunks
}

func receiveSnapshotChunks(t *testing.T, stream grpc.BidiStreamingClient[gatewayv2.AgentMessage, gatewayv2.ControllerMessage], header *gatewayv2.SnapshotHeader) []*gatewayv2.SnapshotChunk {
	t.Helper()
	chunks := make([]*gatewayv2.SnapshotChunk, 0, header.ChunkCount)
	for int32(len(chunks)) < header.ChunkCount {
		message, err := stream.Recv()
		if err != nil {
			t.Fatal(err)
		}
		if chunk := message.GetSnapshotChunk(); chunk != nil {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

func waitForGatewayACK(t *testing.T, ctx context.Context, store *storage.SQLStore, gatewayID string, revision int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		gateway, err := store.GetGateway(ctx, gatewayID)
		if err == nil && gateway.AppliedRevision == revision && gateway.Status == model.GatewayReady {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("gateway ACK was not persisted")
}
