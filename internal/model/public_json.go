package model

import "encoding/json"

type publicDomainTLS struct {
	Mode           TLSMode           `json:"mode"`
	CertificateID  string            `json:"certificate_id,omitempty"`
	ACMEChallenge  ACMEChallengeType `json:"acme_challenge,omitempty"`
	DNSProviderRef string            `json:"dns_provider_ref,omitempty"`
}

// MarshalJSON keeps persistence-oriented columns private and exposes the v2
// API contract where entrypoints and TLS are independent concepts.
func (d Domain) MarshalJSON() ([]byte, error) {
	entrypoints := make([]string, 0, 2)
	if d.HTTPEnabled {
		entrypoints = append(entrypoints, "http")
	}
	if d.HTTPSEnabled {
		entrypoints = append(entrypoints, "https")
	}
	return json.Marshal(struct {
		ID                  string          `json:"id"`
		PoolID              string          `json:"pool_id"`
		Host                string          `json:"host"`
		EntryPoints         []string        `json:"entrypoints"`
		RedirectHTTPToHTTPS bool            `json:"redirect_http_to_https"`
		HTTP3Enabled        bool            `json:"http3_enabled"`
		TLS                 publicDomainTLS `json:"tls"`
		Status              DomainStatus    `json:"status"`
		Source              Source          `json:"source"`
		Enabled             bool            `json:"enabled"`
		LastError           string          `json:"last_error,omitempty"`
		Metadata
	}{
		ID: d.ID, PoolID: d.PoolID, Host: d.Host, EntryPoints: entrypoints,
		RedirectHTTPToHTTPS: d.HTTPSRedirect, HTTP3Enabled: d.HTTP3Enabled,
		TLS:    publicDomainTLS{Mode: d.TLSMode, CertificateID: d.CertificateID, ACMEChallenge: d.ACMEChallenge, DNSProviderRef: d.DNSProviderRef},
		Status: d.Status, Source: d.Source, Enabled: d.Enabled, LastError: d.LastError, Metadata: d.Metadata,
	})
}

func (d *Domain) UnmarshalJSON(data []byte) error {
	var value struct {
		ID                  string          `json:"id"`
		PoolID              string          `json:"pool_id"`
		Host                string          `json:"host"`
		EntryPoints         []string        `json:"entrypoints"`
		RedirectHTTPToHTTPS bool            `json:"redirect_http_to_https"`
		HTTP3Enabled        bool            `json:"http3_enabled"`
		TLS                 publicDomainTLS `json:"tls"`
		Status              DomainStatus    `json:"status"`
		Source              Source          `json:"source"`
		Enabled             bool            `json:"enabled"`
		LastError           string          `json:"last_error"`
		Metadata
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	d.ID, d.PoolID, d.Host = value.ID, value.PoolID, value.Host
	d.HTTPSRedirect = value.RedirectHTTPToHTTPS
	d.HTTP3Enabled = value.HTTP3Enabled
	d.HTTPEnabled, d.HTTPSEnabled = false, false
	d.TLSMode, d.CertificateID = value.TLS.Mode, value.TLS.CertificateID
	d.ACMEChallenge, d.DNSProviderRef = value.TLS.ACMEChallenge, value.TLS.DNSProviderRef
	d.Status, d.Source, d.Enabled, d.LastError, d.Metadata = value.Status, value.Source, value.Enabled, value.LastError, value.Metadata
	for _, entrypoint := range value.EntryPoints {
		switch entrypoint {
		case "http":
			d.HTTPEnabled = true
		case "https":
			d.HTTPSEnabled = true
		}
	}
	return nil
}

type publicRouteBackend struct {
	ServiceID  string `json:"service_id"`
	Port       int    `json:"port"`
	Protocol   string `json:"protocol"`
	ServerName string `json:"server_name,omitempty"`
}

// MarshalJSON exposes a backend object so a route's client entrypoint cannot
// be confused with its upstream transport protocol.
func (r Route) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID               string             `json:"id"`
		DomainID         string             `json:"domain_id"`
		Kind             string             `json:"kind"`
		Match            string             `json:"match"`
		ExternalURL      string             `json:"external_url,omitempty"`
		ResponseCode     int                `json:"response_code,omitempty"`
		ResponseValue    string             `json:"response_value,omitempty"`
		ReadTimeout      int                `json:"read_timeout_seconds,omitempty"`
		ConnectTimeout   int                `json:"connect_timeout_seconds,omitempty"`
		ExternalKey      string             `json:"external_key,omitempty"`
		Path             string             `json:"path"`
		Priority         int                `json:"priority"`
		StripPath        bool               `json:"strip_path"`
		WebsocketEnabled bool               `json:"websocket_enabled"`
		Backend          publicRouteBackend `json:"backend"`
		Enabled          bool               `json:"enabled"`
		Status           RouteStatus        `json:"status"`
		Source           Source             `json:"source"`
		LastError        string             `json:"last_error,omitempty"`
		Metadata
	}{
		ID: r.ID, DomainID: r.DomainID, Kind: r.Kind, Match: r.Match, ExternalURL: r.ExternalURL,
		ResponseCode: r.ResponseCode, ResponseValue: r.ResponseValue, ReadTimeout: r.ReadTimeoutSeconds, ConnectTimeout: r.ConnectTimeoutSeconds,
		ExternalKey: r.ExternalKey, Path: r.Path, Priority: r.Priority,
		StripPath: r.StripPath, WebsocketEnabled: r.WebsocketEnabled,
		Backend: publicRouteBackend{ServiceID: r.ServiceID, Port: r.TargetPort, Protocol: r.UpstreamProtocol, ServerName: r.UpstreamServerName},
		Enabled: r.Enabled, Status: r.Status, Source: r.Source, LastError: r.LastError, Metadata: r.Metadata,
	})
}

func (r *Route) UnmarshalJSON(data []byte) error {
	var value struct {
		ID               string             `json:"id"`
		DomainID         string             `json:"domain_id"`
		Kind             string             `json:"kind"`
		Match            string             `json:"match"`
		ExternalURL      string             `json:"external_url"`
		ResponseCode     int                `json:"response_code"`
		ResponseValue    string             `json:"response_value"`
		ReadTimeout      int                `json:"read_timeout_seconds"`
		ConnectTimeout   int                `json:"connect_timeout_seconds"`
		ExternalKey      string             `json:"external_key"`
		Path             string             `json:"path"`
		Priority         int                `json:"priority"`
		StripPath        bool               `json:"strip_path"`
		WebsocketEnabled bool               `json:"websocket_enabled"`
		Backend          publicRouteBackend `json:"backend"`
		Enabled          bool               `json:"enabled"`
		Status           RouteStatus        `json:"status"`
		Source           Source             `json:"source"`
		LastError        string             `json:"last_error"`
		Metadata
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	r.ID, r.DomainID, r.ExternalKey = value.ID, value.DomainID, value.ExternalKey
	r.Kind, r.Match, r.ExternalURL = value.Kind, value.Match, value.ExternalURL
	r.ResponseCode, r.ResponseValue = value.ResponseCode, value.ResponseValue
	r.ReadTimeoutSeconds, r.ConnectTimeoutSeconds = value.ReadTimeout, value.ConnectTimeout
	r.Path, r.Priority, r.StripPath, r.WebsocketEnabled = value.Path, value.Priority, value.StripPath, value.WebsocketEnabled
	r.ServiceID, r.TargetPort = value.Backend.ServiceID, value.Backend.Port
	r.UpstreamProtocol, r.UpstreamServerName = value.Backend.Protocol, value.Backend.ServerName
	r.Enabled, r.Status, r.Source, r.LastError, r.Metadata = value.Enabled, value.Status, value.Source, value.LastError, value.Metadata
	return nil
}
