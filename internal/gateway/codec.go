package gateway

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"time"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/model"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultChunkSize    = 512 * 1024
	MaxCompressedSize   = 64 * 1024 * 1024
	MaxUncompressedSize = 256 * 1024 * 1024
)

type EncodedSnapshot struct {
	Header *gatewayv2.SnapshotHeader
	Chunks []*gatewayv2.SnapshotChunk
}

func EncodeSnapshot(snapshot model.Snapshot, chunkSize int) (EncodedSnapshot, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	message := SnapshotToProto(snapshot)
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return EncodedSnapshot{}, fmt.Errorf("marshal snapshot: %w", err)
	}
	if len(raw) > MaxUncompressedSize {
		return EncodedSnapshot{}, fmt.Errorf("snapshot exceeds %d uncompressed bytes", MaxUncompressedSize)
	}
	modelSum := sha256.Sum256(raw)
	var compressed bytes.Buffer
	writer, err := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
	if err != nil {
		return EncodedSnapshot{}, err
	}
	writer.Header.ModTime = time.Unix(0, 0)
	writer.Header.OS = 255
	if _, err = writer.Write(raw); err != nil {
		return EncodedSnapshot{}, err
	}
	if err = writer.Close(); err != nil {
		return EncodedSnapshot{}, err
	}
	if compressed.Len() > MaxCompressedSize {
		return EncodedSnapshot{}, fmt.Errorf("snapshot exceeds %d compressed bytes", MaxCompressedSize)
	}
	sum := sha256.Sum256(compressed.Bytes())
	count := (compressed.Len() + chunkSize - 1) / chunkSize
	encoded := EncodedSnapshot{Header: &gatewayv2.SnapshotHeader{ConfigRevision: snapshot.Revision,
		ModelDigest: hex.EncodeToString(modelSum[:]), TransportDigest: hex.EncodeToString(sum[:]),
		UncompressedSize: int64(len(raw)), CompressedSize: int64(compressed.Len()), ChunkCount: int32(count)}}
	for index, offset := 0, 0; offset < compressed.Len(); index, offset = index+1, offset+chunkSize {
		end := min(offset+chunkSize, compressed.Len())
		data := append([]byte(nil), compressed.Bytes()[offset:end]...)
		encoded.Chunks = append(encoded.Chunks, &gatewayv2.SnapshotChunk{ConfigRevision: snapshot.Revision, Index: int32(index), Data: data})
	}
	return encoded, nil
}

func DecodeSnapshot(header *gatewayv2.SnapshotHeader, chunks []*gatewayv2.SnapshotChunk) (*gatewayv2.Snapshot, error) {
	if header == nil || header.ConfigRevision <= 0 {
		return nil, fmt.Errorf("invalid snapshot header")
	}
	if header.CompressedSize < 0 || header.CompressedSize > MaxCompressedSize || header.UncompressedSize < 0 || header.UncompressedSize > MaxUncompressedSize {
		return nil, fmt.Errorf("snapshot size limit exceeded")
	}
	if int(header.ChunkCount) != len(chunks) || header.ChunkCount < 1 {
		return nil, fmt.Errorf("snapshot chunk count mismatch")
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Index < chunks[j].Index })
	var compressed bytes.Buffer
	for index, chunk := range chunks {
		if chunk.ConfigRevision != header.ConfigRevision || chunk.Index != int32(index) {
			return nil, fmt.Errorf("invalid snapshot chunk %d", index)
		}
		if compressed.Len()+len(chunk.Data) > MaxCompressedSize {
			return nil, fmt.Errorf("snapshot compressed size limit exceeded")
		}
		compressed.Write(chunk.Data)
	}
	if int64(compressed.Len()) != header.CompressedSize {
		return nil, fmt.Errorf("snapshot compressed size mismatch")
	}
	sum := sha256.Sum256(compressed.Bytes())
	if hex.EncodeToString(sum[:]) != header.TransportDigest {
		return nil, fmt.Errorf("snapshot checksum mismatch")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		return nil, fmt.Errorf("open snapshot gzip: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxUncompressedSize+1))
	closeErr := reader.Close()
	if err != nil {
		return nil, fmt.Errorf("decompress snapshot: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close snapshot gzip: %w", closeErr)
	}
	if len(raw) > MaxUncompressedSize || int64(len(raw)) != header.UncompressedSize {
		return nil, fmt.Errorf("snapshot uncompressed size mismatch")
	}
	var result gatewayv2.Snapshot
	if err = proto.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}
	if result.ConfigRevision != header.ConfigRevision {
		return nil, fmt.Errorf("snapshot revision mismatch")
	}
	modelSum := sha256.Sum256(raw)
	if hex.EncodeToString(modelSum[:]) != header.ModelDigest {
		return nil, fmt.Errorf("snapshot model checksum mismatch")
	}
	return &result, nil
}

// SnapshotToProto exposes the exact v2 wire model to trusted in-process
// diagnostics. It must not be serialized directly because it includes private
// key material for Gateway delivery.
func SnapshotToProto(snapshot model.Snapshot) *gatewayv2.Snapshot {
	result := &gatewayv2.Snapshot{PoolId: snapshot.PoolID, ConfigRevision: snapshot.Revision, GeneratedAtUnixNano: snapshot.GeneratedAt.UnixNano(), ChallengeUpstream: snapshot.ChallengeUpstream}
	for _, acl := range snapshot.AccessControls {
		value := &gatewayv2.IPAccessControl{Scope: acl.Scope, ResourceId: acl.ResourceID, Access: acl.Access, Forbidden: acl.Forbidden}
		if acl.ClientIP != nil {
			value.ClientIp = &gatewayv2.ClientIPSource{Header: acl.ClientIP.Header, TrustedProxies: acl.ClientIP.TrustedProxies}
		}
		result.AccessControls = append(result.AccessControls, value)
	}
	for _, snippet := range snapshot.Snippets {
		result.Snippets = append(result.Snippets, &gatewayv2.ConfigSnippet{Scope: snippet.Scope, ResourceId: snippet.ResourceID, Content: snippet.Content})
	}
	for _, domain := range snapshot.Domains {
		result.Domains = append(result.Domains, &gatewayv2.SnapshotDomain{Id: domain.ID, Host: domain.Host,
			HttpEnabled: domain.HTTPEnabled, HttpsEnabled: domain.HTTPSEnabled, Http3Enabled: domain.HTTP3Enabled, RedirectHttpToHttps: domain.HTTPSRedirect,
			TlsMode: string(domain.TLSMode), CertificateId: domain.CertificateID, AcmeChallenge: string(domain.ACMEChallenge)})
	}
	for _, route := range snapshot.Routes {
		result.Routes = append(result.Routes, &gatewayv2.SnapshotRoute{Id: route.ID, Host: route.Host, Path: route.Path,
			Priority: int32(route.Priority), StripPath: route.StripPath, ServiceId: route.ServiceID,
			TargetPort: int32(route.TargetPort), WebsocketEnabled: route.WebsocketEnabled,
			UpstreamProtocol: route.UpstreamProtocol, UpstreamServerName: route.UpstreamServerName, UpstreamName: route.UpstreamName,
			Paused: route.Paused, Kind: route.Kind, Match: route.Match, ExternalUrl: route.ExternalURL,
			ResponseCode: int32(route.ResponseCode), ResponseValue: route.ResponseValue,
			ReadTimeoutSeconds: int32(route.ReadTimeoutSeconds), ConnectTimeoutSeconds: int32(route.ConnectTimeoutSeconds)})
	}
	serviceIDs := make([]string, 0, len(snapshot.Services))
	for serviceID := range snapshot.Services {
		serviceIDs = append(serviceIDs, serviceID)
	}
	sort.Strings(serviceIDs)
	for _, serviceID := range serviceIDs {
		service := snapshot.Services[serviceID]
		converted := &gatewayv2.SnapshotService{Id: service.ID, QualifiedName: service.QualifiedName, DnsName: service.DNSName}
		result.Services = append(result.Services, converted)
	}
	certificateIDs := make([]string, 0, len(snapshot.Certificates))
	for certificateID := range snapshot.Certificates {
		certificateIDs = append(certificateIDs, certificateID)
	}
	sort.Strings(certificateIDs)
	for _, certificateID := range certificateIDs {
		certificate := snapshot.Certificates[certificateID]
		result.Certificates = append(result.Certificates, &gatewayv2.SnapshotCertificate{Id: certificate.ID, Domains: certificate.Domains, CertificatePem: certificate.CertificatePEM, PrivateKeyPem: certificate.PrivateKeyPEM})
	}
	return result
}

func ProbePlanToProto(plan model.ProbePlan) *gatewayv2.ProbePlan {
	result := &gatewayv2.ProbePlan{PoolId: plan.PoolID, Epoch: plan.Epoch, ProbeRevision: plan.Revision}
	for _, target := range plan.Targets {
		policy := target.Policy
		result.Targets = append(result.Targets, &gatewayv2.ProbeTarget{ServiceId: target.ServiceID, DnsName: target.DNSName,
			Port: int32(target.Port), Mode: string(policy.Mode), Path: policy.Path, Scheme: policy.Scheme,
			IntervalNanos: int64(policy.Interval), TimeoutNanos: int64(policy.Timeout),
			SuccessThreshold: int32(policy.SuccessThreshold), FailureThreshold: int32(policy.FailureThreshold), TargetToken: target.TargetToken})
	}
	return result
}
