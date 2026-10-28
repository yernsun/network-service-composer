package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/upstream"
)

type snapshotCandidate struct {
	domain  model.Domain
	route   model.Route
	service model.Service
}

func (s *SQLStore) BuildSnapshot(ctx context.Context, poolID string) (model.Snapshot, error) {
	for i := 0; i < 5; i++ {
		snapshot, e := s.buildSnapshot(ctx, poolID)
		if e != nil {
			return snapshot, e
		}
		pool, e := s.GetPool(ctx, poolID)
		if e != nil {
			return snapshot, e
		}
		if pool.DesiredRevision == snapshot.Revision {
			return snapshot, nil
		}
	}
	return model.Snapshot{}, ErrConflict
}
func (s *SQLStore) buildSnapshot(ctx context.Context, poolID string) (model.Snapshot, error) {
	pool, err := s.GetPool(ctx, poolID)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot := model.Snapshot{
		// The payload for a revision must remain byte-for-byte reproducible even
		// when non-data-plane pool metadata (heartbeats or probe leadership)
		// changes. The actual build time is stored separately in pool_snapshots.
		PoolID: poolID, Revision: pool.DesiredRevision, GeneratedAt: time.Unix(0, 0).UTC(),
		ChallengeUpstream: pool.ChallengeUpstream,
		Services:          make(map[string]model.SnapshotService), Certificates: make(map[string]model.SnapshotCertificate),
	}
	if d, e := s.GetControlDocument(ctx, poolID+":nginx-active"); e == nil {
		var active struct {
			Snippets       []model.ConfigSnippet   `json:"snippets"`
			AccessControls []model.IPAccessControl `json:"access_controls"`
		}
		if e = json.Unmarshal(d.Payload, &active); e != nil {
			return model.Snapshot{}, e
		}
		snapshot.Snippets = active.Snippets
		snapshot.AccessControls = active.AccessControls
	} else if !errors.Is(e, ErrNotFound) {
		return model.Snapshot{}, e
	}
	if !pool.Enabled {
		return snapshot, nil
	}
	domainsByID := make(map[string]model.Domain)
	domainRows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+domainColumns+` FROM domains WHERE pool_id=:pool_id AND enabled=:enabled ORDER BY host,id`), poolID, true)
	if err != nil {
		return model.Snapshot{}, err
	}
	for domainRows.Next() {
		domain, scanErr := scanDomain(domainRows)
		if scanErr != nil {
			domainRows.Close()
			return model.Snapshot{}, scanErr
		}
		domainsByID[domain.ID] = domain
		snapshot.Domains = append(snapshot.Domains, model.SnapshotDomain{ID: domain.ID, Host: domain.Host, TLSMode: domain.TLSMode, ACMEChallenge: domain.ACMEChallenge,
			HTTPSRedirect: domain.HTTPSRedirect, HTTPEnabled: domain.HTTPEnabled, HTTPSEnabled: domain.HTTPSEnabled, HTTP3Enabled: domain.HTTP3Enabled,
			CertificateID: domain.CertificateID})
	}
	if err = domainRows.Close(); err != nil {
		return model.Snapshot{}, err
	}
	if err = domainRows.Err(); err != nil {
		return model.Snapshot{}, err
	}
	serviceRows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+serviceColumns+` FROM services
		WHERE runtime_id IN (SELECT id FROM runtimes WHERE pool_id=:pool_id AND enabled=:enabled) ORDER BY id`), poolID, true)
	if err != nil {
		return model.Snapshot{}, err
	}
	servicesByID := make(map[string]model.Service)
	for serviceRows.Next() {
		service, scanErr := scanService(serviceRows)
		if scanErr != nil {
			serviceRows.Close()
			return model.Snapshot{}, scanErr
		}
		servicesByID[service.ID] = service
	}
	if err = serviceRows.Close(); err != nil {
		return model.Snapshot{}, err
	}
	if err = serviceRows.Err(); err != nil {
		return model.Snapshot{}, err
	}

	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT r.id,r.domain_id,r.service_id,r.external_key,r.path,r.priority,r.strip_path,
		r.target_port,r.upstream_protocol,r.upstream_server_name,r.websocket_enabled,r.enabled,r.route_status,r.source,
		r.last_error,r.revision,r.created_at,r.updated_at,r.kind,r.match_kind,r.external_url,r.response_code,r.response_value,
		r.read_timeout_seconds,r.connect_timeout_seconds FROM routes r JOIN domains d ON d.id=r.domain_id
		LEFT JOIN services sv ON sv.id=r.service_id LEFT JOIN runtimes rt ON rt.id=sv.runtime_id
		WHERE d.pool_id=:pool_id AND (r.service_id IS NULL OR rt.pool_id=:pool_id_2) AND d.enabled=:enabled AND ((r.enabled=:enabled_2 AND r.route_status=:route_status) OR r.route_status=:route_status_2) AND (r.service_id IS NULL OR rt.enabled=:enabled_3)`),
		poolID, poolID, true, true, model.RouteActive, model.RoutePaused, true)
	if err != nil {
		return model.Snapshot{}, err
	}
	var candidates []snapshotCandidate
	for rows.Next() {
		route, scanErr := scanRoute(rows)
		if scanErr != nil {
			rows.Close()
			return model.Snapshot{}, scanErr
		}
		domain, domainOK := domainsByID[route.DomainID]
		service, serviceOK := servicesByID[route.ServiceID]
		static := route.Kind == "return" || route.Kind == "redirect"
		external := route.Kind == "proxy" && route.ExternalURL != ""
		activeEligible := route.Status == model.RouteActive && (static || external || (serviceRouteEnabled(service) && service.ConfigurationError == "" && service.DNSStatus != model.DNSConflict))
		if domainOK && (static || external || serviceOK && service.EffectiveDNSName() != "") && (activeEligible || route.Status == model.RoutePaused) {
			candidates = append(candidates, snapshotCandidate{domain: domain, route: route, service: service})
		}
	}
	if err := rows.Close(); err != nil {
		return model.Snapshot{}, err
	}
	if err := rows.Err(); err != nil {
		return model.Snapshot{}, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].domain.Host != candidates[j].domain.Host {
			return candidates[i].domain.Host < candidates[j].domain.Host
		}
		if len(candidates[i].route.Path) != len(candidates[j].route.Path) {
			return len(candidates[i].route.Path) > len(candidates[j].route.Path)
		}
		if candidates[i].route.Source.Priority() != candidates[j].route.Source.Priority() {
			return candidates[i].route.Source.Priority() > candidates[j].route.Source.Priority()
		}
		if candidates[i].route.Priority != candidates[j].route.Priority {
			return candidates[i].route.Priority > candidates[j].route.Priority
		}
		return candidates[i].route.ID < candidates[j].route.ID
	})
	seenLocations := make(map[string]struct{})
	upstreamSignatures := make(map[string]string)
	for _, candidate := range candidates {
		key := candidate.domain.Host + "\x00" + candidate.route.Path + "\x00" + candidate.route.Match
		if _, exists := seenLocations[key]; exists {
			continue
		}
		seenLocations[key] = struct{}{}
		port := candidate.route.TargetPort
		if port == 0 {
			port = candidate.service.EffectivePort()
		}
		protocol := candidate.route.UpstreamProtocol
		if protocol == "" {
			protocol = candidate.service.EffectiveProtocol()
		}
		qualifiedName := candidate.service.EffectiveQualifiedName()
		upstreamName := ""
		if candidate.route.Kind == "proxy" && candidate.route.ExternalURL == "" {
			var nameErr error
			upstreamName, nameErr = upstream.Name(candidate.service.RuntimeID, candidate.service.ServiceKey, qualifiedName, port, protocol, candidate.route.UpstreamServerName)
			if nameErr != nil {
				return model.Snapshot{}, fmt.Errorf("route %s upstream identity: %w", candidate.route.ID, nameErr)
			}
			signature := candidate.service.RuntimeID + "\x00" + candidate.service.ServiceKey + "\x00" + candidate.service.EffectiveDNSName() + fmt.Sprintf("\x00%d\x00%s\x00%s", port, protocol, candidate.route.UpstreamServerName)
			if previous, exists := upstreamSignatures[upstreamName]; exists && previous != signature {
				return model.Snapshot{}, fmt.Errorf("upstream name collision for %s", upstreamName)
			}
			upstreamSignatures[upstreamName] = signature
		}
		snapshot.Routes = append(snapshot.Routes, model.SnapshotRoute{
			ID: candidate.route.ID, Host: candidate.domain.Host, Path: candidate.route.Path,
			Kind: candidate.route.Kind, Match: candidate.route.Match, ExternalURL: candidate.route.ExternalURL,
			ResponseCode: candidate.route.ResponseCode, ResponseValue: candidate.route.ResponseValue,
			ReadTimeoutSeconds: candidate.route.ReadTimeoutSeconds, ConnectTimeoutSeconds: candidate.route.ConnectTimeoutSeconds,
			Priority: candidate.route.Priority, StripPath: candidate.route.StripPath, ServiceID: candidate.service.ID,
			TargetPort: port, TLSMode: candidate.domain.TLSMode, HTTPSRedirect: candidate.domain.HTTPSRedirect,
			CertificateID: candidate.domain.CertificateID, WebsocketEnabled: candidate.route.WebsocketEnabled,
			UpstreamProtocol: protocol, UpstreamServerName: candidate.route.UpstreamServerName, UpstreamName: upstreamName,
			Paused: candidate.route.Status == model.RoutePaused,
		})
		if candidate.route.Kind == "proxy" && candidate.route.ExternalURL == "" && snapshot.Services[candidate.service.ID].ID == "" {
			service := model.SnapshotService{ID: candidate.service.ID, QualifiedName: qualifiedName,
				DNSName: candidate.service.EffectiveDNSName(), Protocol: candidate.service.EffectiveProtocol()}
			snapshot.Services[candidate.service.ID] = service
		}
	}
	referencedCertificates := make(map[string]struct{})
	for _, domain := range snapshot.Domains {
		if domain.CertificateID != "" {
			referencedCertificates[domain.CertificateID] = struct{}{}
		}
	}
	certificates, err := s.snapshotCertificates(ctx, poolID, referencedCertificates)
	if err != nil {
		return model.Snapshot{}, err
	}
	for certificateID, certificate := range certificates {
		snapshot.Certificates[certificateID] = certificate
	}
	return snapshot, nil
}

func (s *SQLStore) snapshotCertificates(ctx context.Context, poolID string, referenced map[string]struct{}) (map[string]model.SnapshotCertificate, error) {
	if len(referenced) == 0 {
		return map[string]model.SnapshotCertificate{}, nil
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT c.id,c.certificate_pem,d.host
		FROM certificates c LEFT JOIN certificate_domains cd ON cd.certificate_id=c.id
		LEFT JOIN domains d ON d.id=cd.domain_id
		WHERE c.pool_id=:pool_id AND c.status IN (:active_status,:renewing_status) ORDER BY c.id,d.host`), poolID, model.CertificateActive, model.CertificateRenewing)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]model.SnapshotCertificate)
	for rows.Next() {
		var certificateID, certificatePEM string
		var host sql.NullString
		if err = rows.Scan(&certificateID, &certificatePEM, &host); err != nil {
			return nil, err
		}
		if _, ok := referenced[certificateID]; !ok {
			continue
		}
		value := values[certificateID]
		value.ID = certificateID
		value.CertificatePEM = certificatePEM
		if host.Valid {
			value.Domains = append(value.Domains, host.String)
		}
		values[certificateID] = value
	}
	return values, rows.Err()
}

func (s *SQLStore) BuildProbePlan(ctx context.Context, poolID string) (model.ProbePlan, error) {
	pool, err := s.GetPool(ctx, poolID)
	if err != nil {
		return model.ProbePlan{}, err
	}
	plan := model.ProbePlan{PoolID: poolID, Epoch: pool.ProbeEpoch, Revision: pool.ProbeRevision}

	if !pool.Enabled {
		return plan, nil
	}
	rows, err := s.db.QueryContext(ctx, s.bind(`SELECT `+serviceColumns+` FROM services
		WHERE runtime_id IN (SELECT id FROM runtimes WHERE pool_id=:pool_id AND enabled=:enabled)
		AND (dns_status<>:dns_status OR EXISTS (SELECT 1 FROM routes probe_route WHERE probe_route.service_id=services.id
			AND probe_route.enabled=:enabled_2 AND probe_route.route_status=:route_status)) ORDER BY id`),
		poolID, true, model.DNSMissing, true, model.RouteActive)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	for rows.Next() {
		service, scanErr := scanService(rows)
		if scanErr != nil {
			return plan, scanErr
		}
		address := service.EffectiveDNSName()
		port := service.EffectivePort()
		if !serviceRouteEnabled(service) || service.ConfigurationError != "" || address == "" || net.ParseIP(address) != nil ||
			service.DNSStatus == model.DNSConflict || port < 1 || port > 65535 {
			continue
		}
		policy := normalizedDNSProbePolicy(service.EffectiveReadiness())
		if policy.Mode != model.ReadinessHTTP && policy.Mode != model.ReadinessTCP {
			continue
		}
		token := serviceDNSProbeToken(policy, address, port)
		if token == "" {
			continue
		}
		plan.Targets = append(plan.Targets, model.ProbeTarget{ServiceID: service.ID,
			DNSName: address, Port: port, Policy: policy, TargetToken: token})
	}
	if err = rows.Err(); err != nil {
		return plan, err
	}
	return plan, nil
}

// Discovery eligibility gates creation/attachment, not the lifetime of an
// already active DNS route. Once configured, transient runtime unavailability
// must leave the upstream in place so Docker DNS can recover without reload.
func serviceRouteEnabled(service model.Service) bool {
	return service.EnabledOverride == nil || *service.EnabledOverride
}

func normalizedDNSProbePolicy(policy model.ReadinessPolicy) model.ReadinessPolicy {
	if policy.Mode == model.ReadinessAuto {
		if policy.Path != "" {
			policy.Mode = model.ReadinessHTTP
		} else if policy.TCPFallback {
			policy.Mode = model.ReadinessTCP
		}
	} else if (policy.Mode == model.ReadinessDocker || policy.Mode == model.ReadinessGrace) && policy.TCPFallback {
		policy.Mode = model.ReadinessTCP
	}
	if policy.Mode == model.ReadinessHTTP {
		if policy.Scheme == "" {
			policy.Scheme = "http"
		}
		if policy.Path == "" {
			policy.Path = "/"
		}
	}
	return policy
}

type nullableString struct {
	value string
	valid bool
}

func (n *nullableString) Scan(src any) error {
	var v sqlNullString
	if err := v.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = v.value, v.valid
	return nil
}
func (n nullableString) String() string {
	if !n.valid {
		return ""
	}
	return n.value
}
func (n nullableString) StringPtr() *string {
	if !n.valid {
		return nil
	}
	value := n.value
	return &value
}

type nullableInt struct {
	value int64
	valid bool
}

func (n *nullableInt) Scan(src any) error {
	var v sqlNullInt
	if err := v.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = v.value, v.valid
	return nil
}
func (n nullableInt) IntPtr() *int {
	if !n.valid {
		return nil
	}
	value := int(n.value)
	return &value
}

type nullableBool struct {
	value bool
	valid bool
}

func (n *nullableBool) Scan(src any) error {
	var v sqlNullBool
	if err := v.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = v.value, v.valid
	return nil
}
func (n nullableBool) BoolPtr() *bool {
	if !n.valid {
		return nil
	}
	value := n.value
	return &value
}

type nullableTime struct {
	value time.Time
	valid bool
}

func (n *nullableTime) Scan(src any) error {
	var v sqlNullTime
	if err := v.Scan(src); err != nil {
		return err
	}
	n.value, n.valid = v.value, v.valid
	return nil
}
func (n nullableTime) TimePtr() *time.Time {
	if !n.valid {
		return nil
	}
	value := n.value
	return &value
}
