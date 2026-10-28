package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/yernsun/network-service-composer/internal/id"
	"github.com/yernsun/network-service-composer/internal/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestGatewayIdentityRequiresVerifiedMTLSAndValidResourceIDs(t *testing.T) {
	pool, gateway := id.New(id.Pool), id.New(id.Gateway)
	identityURI, err := AgentIdentityURI(pool, gateway)
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]string{{"bad", gateway}, {pool, "bad"}, {gateway, pool}} {
		if _, err := AgentIdentityURI(ids[0], ids[1]); err == nil {
			t.Fatalf("invalid identity created: %v", ids)
		}
	}
	for _, uri := range []*url.URL{
		nil, {Scheme: "https", Host: identityHost, Path: identityURI.Path}, {Scheme: "spiffe", Host: "other", Path: identityURI.Path},
		{Scheme: "spiffe", Host: identityHost, Path: "/gateway-pools"}, {Scheme: "spiffe", Host: identityHost, Path: "/other/" + pool + "/gateways/" + gateway},
		{Scheme: "spiffe", Host: identityHost, Path: "/gateway-pools/" + pool + "/other/" + gateway}, {Scheme: "spiffe", Host: identityHost, Path: "/gateway-pools//gateways/" + gateway},
		{Scheme: "spiffe", Host: identityHost, Path: "/gateway-pools/invalid/gateways/" + gateway}, {Scheme: "spiffe", Host: identityHost, Path: "/gateway-pools/" + pool + "/gateways/invalid"},
	} {
		if identity, ok := parseIdentityURI(uri); ok || identity.PoolID != "" || identity.GatewayID != "" {
			t.Fatalf("invalid URI accepted: %v", uri)
		}
	}
	certificate := &x509.Certificate{Raw: []byte("verified certificate fixture"), URIs: []*url.URL{{Scheme: "https", Host: "unrelated"}, identityURI}}
	validTLS := credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}, VerifiedChains: [][]*x509.Certificate{{certificate}}}}
	for _, ctx := range []context.Context{
		context.Background(), peer.NewContext(context.Background(), &peer.Peer{}),
		peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}}}),
		peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{certificate}}}}}),
		peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{certificate}}, PeerCertificates: []*x509.Certificate{{}}}}}),
	} {
		identity, fingerprint, err := identityFromContext(ctx)
		if err == nil || identity.GatewayID != "" || fingerprint != "" {
			t.Fatalf("unverified client accepted: %#v %q %v", identity, fingerprint, err)
		}
	}
	identity, fingerprint, err := identityFromContext(peer.NewContext(context.Background(), &peer.Peer{AuthInfo: validTLS}))
	digest := sha256.Sum256(certificate.Raw)
	if err != nil || identity.PoolID != pool || identity.GatewayID != gateway || fingerprint != hex.EncodeToString(digest[:]) {
		t.Fatalf("verified identity incorrect: %#v %q %v", identity, fingerprint, err)
	}
}

func TestGatewayStateErrorsDoNotExposeInternalDetails(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    codes.Code
		message string
	}{
		{errGatewayRevoked, codes.PermissionDenied, errGatewayRevoked.Error()}, {fmt.Errorf("wrapped: %w", storage.ErrNotFound), codes.PermissionDenied, errGatewayRevoked.Error()},
		{errors.New("secret internal connection parameters"), codes.Internal, "read state"},
	} {
		err := connectStateError(tc.err, "read state")
		if status.Code(err) != tc.code || status.Convert(err).Message() != tc.message {
			t.Fatalf("unsafe protocol error: %v", err)
		}
	}
	if truncate("short", 10) != "short" || truncate("long value", 4) != "long" {
		t.Fatal("protocol message truncation mismatch")
	}
	for _, input := range []string{"", strings.Repeat("a", 63), strings.Repeat("A", 64), strings.Repeat("z", 64)} {
		if validDigest(input) {
			t.Fatalf("invalid digest accepted: %q", input)
		}
	}
	if !validDigest(strings.Repeat("0", 64)) {
		t.Fatal("valid digest rejected")
	}
}
