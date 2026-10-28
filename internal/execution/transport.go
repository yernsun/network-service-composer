package execution

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/yernsun/network-service-composer/internal/config"
	"github.com/yernsun/network-service-composer/internal/controlplane"
)

const InternalPath = "/internal/v1/operation-execution"

func Dispatch(ctx context.Context, transportTLS *tls.Config, binding config.ControllerNodeConfig, request Request) error {
	if transportTLS == nil || transportTLS.VerifyConnection == nil || binding.InstanceID == "" || controlplane.ValidateInternalURL(binding.URL) != nil {
		return ErrFenced
	}
	tlsConfig := transportTLS.Clone()
	verify := tlsConfig.VerifyConnection
	tlsConfig.VerifyConnection = func(state tls.ConnectionState) error {
		if err := verify(state); err != nil {
			return err
		}
		if len(state.PeerCertificates) == 0 {
			return ErrFenced
		}
		identity, err := controlplane.CertificateIdentity(state.PeerCertificates[0])
		if err != nil || identity != binding.InstanceID {
			return ErrFenced
		}
		return nil
	}
	address, err := url.Parse(binding.URL)
	if err != nil {
		return ErrFenced
	}
	address.Path = InternalPath
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, address.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.GetBody = nil // never automatically replay an acceptance request
	r.Header.Set("Content-Type", "application/json")
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return ErrUnknown
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		return ErrUnknown
	}
	return nil
}
