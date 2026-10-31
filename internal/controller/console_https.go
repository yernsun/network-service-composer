package controller

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
)

// The recovery/first-access listener shares authentication and forwarding with
// HTTP, while its transport remains independent of the Gateway and NGINX.
func consoleHTTPS(cfg config.Config, handler http.Handler) (*http.Server, net.Listener, error) {
	if cfg.Server.ConsoleHTTPSListen == "" {
		return nil, nil, nil
	}
	pair, err := tls.LoadX509KeyPair(cfg.Server.ConsoleHTTPSCertFile, cfg.Server.ConsoleHTTPSKeyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load independent console HTTPS identity: %w", err)
	}
	listener, err := net.Listen("tcp", cfg.Server.ConsoleHTTPSListen)
	if err != nil {
		return nil, nil, fmt.Errorf("listen for independent console HTTPS: %w", err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.Server.ReadTimeout.Std(), WriteTimeout: cfg.Server.WriteTimeout.Std(), IdleTimeout: 60 * time.Second, TLSConfig: tlsConfig}
	return server, tls.NewListener(listener, tlsConfig), nil
}
