package agent

import (
	"context"
	"crypto/tls"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"os/exec"
	"strings"
	"time"
)

func supportsHTTP3(binary string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "-V").CombinedOutput()
	return err == nil && hasHTTP3Module(string(output))
}

// Keep URL Host and TLS SNI while pinning the UDP destination. There is no TCP
// fallback and no proxy or redirect path to a production entrance.
func http3Transport(tlsConfig *tls.Config, address string) *http3.Transport {
	return &http3.Transport{TLSClientConfig: tlsConfig, DisableCompression: true, Dial: func(ctx context.Context, _ string, t *tls.Config, c *quic.Config) (*quic.Conn, error) {
		return quic.DialAddr(ctx, address, t, c)
	}}
}

func hasHTTP3Module(output string) bool {
	for _, flag := range strings.Fields(output) {
		if flag == "--with-http_v3_module" {
			return true
		}
	}
	return false
}
