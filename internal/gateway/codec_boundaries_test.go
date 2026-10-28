package gateway

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/model"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func codecPayload(t *testing.T, raw []byte) EncodedSnapshot {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, e := w.Write(raw); e != nil {
		t.Fatal(e)
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	transport, modelSum := sha256.Sum256(b.Bytes()), sha256.Sum256(raw)
	return EncodedSnapshot{Header: &gatewayv2.SnapshotHeader{ConfigRevision: 7, ChunkCount: 1, CompressedSize: int64(b.Len()), UncompressedSize: int64(len(raw)), TransportDigest: hex.EncodeToString(transport[:]), ModelDigest: hex.EncodeToString(modelSum[:])}, Chunks: []*gatewayv2.SnapshotChunk{{ConfigRevision: 7, Data: b.Bytes()}}}
}
func TestSnapshotDecodingRejectsInconsistentTransportAndModel(t *testing.T) {
	for _, stage := range []string{"nil header", "zero revision", "negative compressed", "large compressed", "negative uncompressed", "large uncompressed", "count mismatch", "zero count", "foreign chunk", "duplicate index", "size mismatch", "transport checksum", "gzip checksum", "plain size mismatch", "invalid protobuf", "model revision", "model checksum"} {
		t.Run(stage, func(t *testing.T) {
			encoded, e := EncodeSnapshot(model.Snapshot{PoolID: "pool", Revision: 7}, 4)
			if e != nil {
				t.Fatal(e)
			}
			h := encoded.Header
			switch stage {
			case "nil header":
				encoded.Header = nil
			case "zero revision":
				h.ConfigRevision = 0
			case "negative compressed":
				h.CompressedSize = -1
			case "large compressed":
				h.CompressedSize = MaxCompressedSize + 1
			case "negative uncompressed":
				h.UncompressedSize = -1
			case "large uncompressed":
				h.UncompressedSize = MaxUncompressedSize + 1
			case "count mismatch":
				h.ChunkCount++
			case "zero count":
				h.ChunkCount = 0
				encoded.Chunks = nil
			case "foreign chunk":
				encoded.Chunks[0].ConfigRevision++
			case "duplicate index":
				encoded.Chunks[1].Index = 0
			case "size mismatch":
				h.CompressedSize++
			case "transport checksum":
				h.TransportDigest = "wrong"
			case "plain size mismatch":
				h.UncompressedSize++
			case "model checksum":
				h.ModelDigest = "wrong"
			case "invalid protobuf":
				encoded = codecPayload(t, []byte{0xff})
			case "model revision":
				raw, e := proto.Marshal(&gatewayv2.Snapshot{PoolId: "pool", ConfigRevision: 8})
				if e != nil {
					t.Fatal(e)
				}
				encoded = codecPayload(t, raw)
			case "gzip checksum":
				raw, e := proto.Marshal(&gatewayv2.Snapshot{ConfigRevision: 7})
				if e != nil {
					t.Fatal(e)
				}
				encoded = codecPayload(t, raw)
				b := encoded.Chunks[0].Data
				b[len(b)-8] ^= 0xff
				sum := sha256.Sum256(b)
				encoded.Header.TransportDigest = hex.EncodeToString(sum[:])
			}
			if snapshot, e := DecodeSnapshot(encoded.Header, encoded.Chunks); e == nil || snapshot != nil {
				t.Fatalf("invalid snapshot accepted: %v %v", snapshot, e)
			}
		})
	}
}
func TestSnapshotDecodingBoundsActualChunkBytesAndSortsChunks(t *testing.T) {
	encoded, e := EncodeSnapshot(model.Snapshot{PoolID: "pool", Revision: 7}, 3)
	if e != nil {
		t.Fatal(e)
	}
	for i, j := 0, len(encoded.Chunks)-1; i < j; i, j = i+1, j-1 {
		encoded.Chunks[i], encoded.Chunks[j] = encoded.Chunks[j], encoded.Chunks[i]
	}
	if snapshot, e := DecodeSnapshot(encoded.Header, encoded.Chunks); e != nil || snapshot.PoolId != "pool" || snapshot.ConfigRevision != 7 {
		t.Fatalf("out-of-order delivery lost content: %v %v", snapshot, e)
	}
	h := &gatewayv2.SnapshotHeader{ConfigRevision: 7, ChunkCount: 1, CompressedSize: 1}
	if snapshot, e := DecodeSnapshot(h, []*gatewayv2.SnapshotChunk{{ConfigRevision: 7, Data: make([]byte, MaxCompressedSize+1)}}); e == nil || snapshot != nil || !strings.Contains(e.Error(), "size limit") {
		t.Fatal("actual chunk limit trusted declared size", e)
	}
	if encoded, e := EncodeSnapshot(model.Snapshot{PoolID: string([]byte{0xff}), Revision: 7}, 0); e == nil || encoded.Header != nil {
		t.Fatal("non-UTF8 wire identity accepted")
	}
}
