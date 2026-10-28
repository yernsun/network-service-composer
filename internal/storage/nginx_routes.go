package storage

import (
	"context"
	"database/sql"
	"time"

	"github.com/yernsun/network-service-composer/internal/model"
)

// writeNginxRoutesTx commits the candidate routes in the same transaction as
// the Pool revision and release head. No staged route is visible before it.
func (s *SQLStore) writeNginxRoutesTx(ctx context.Context, tx *sql.Tx, pool string, writes []RouteWrite) error {
	if len(writes) == 0 {
		return nil
	}
	if pool == "" {
		return ErrPrecondition
	}
	for _, change := range writes {
		route := change.Route
		var domainPool string
		if err := tx.QueryRowContext(ctx, s.bind(`SELECT pool_id FROM domains WHERE id=:id`), route.DomainID).Scan(&domainPool); err != nil {
			return mapError(err)
		}
		if domainPool != pool {
			return ErrConflict
		}
		if route.ServiceID != "" {
			var servicePool string
			if err := tx.QueryRowContext(ctx, s.bind(`SELECT rt.pool_id FROM services sv JOIN runtimes rt ON rt.id=sv.runtime_id WHERE sv.id=:id`), route.ServiceID).Scan(&servicePool); err != nil {
				return mapError(err)
			}
			if servicePool != pool {
				return ErrConflict
			}
		}
		for _, resource := range []string{route.ID, route.DomainID, route.ServiceID} {
			if err := s.guardProjectResourceTx(ctx, tx, resource); err != nil {
				return err
			}
		}
		var currentRevision int64
		var currentDomain string
		var currentSource model.Source
		err := tx.QueryRowContext(ctx, s.bind(`SELECT revision,domain_id,source FROM routes WHERE id=:id`), route.ID).Scan(&currentRevision, &currentDomain, &currentSource)
		if change.ExpectedRevision == 0 {
			if err == nil {
				return ErrPrecondition
			}
			if err != sql.ErrNoRows {
				return err
			}
			if change.Delete {
				continue
			}
			now := time.Now().UTC()
			route.Revision = 1
			route.CreatedAt = now
			route.UpdatedAt = now
			_, err = tx.ExecContext(ctx, s.bind(`INSERT INTO routes (`+routeColumns+`) VALUES (`+columnPlaceholders(routeColumns)+`)`),
				route.ID, route.DomainID, nullString(route.ServiceID), route.ExternalKey, route.Path, route.Priority,
				route.StripPath, route.TargetPort, route.UpstreamProtocol, route.UpstreamServerName,
				route.WebsocketEnabled, route.Enabled, route.Status, route.Source, route.LastError, route.Revision,
				route.CreatedAt, route.UpdatedAt, route.Kind, route.Match, route.ExternalURL,
				route.ResponseCode, route.ResponseValue, route.ReadTimeoutSeconds, route.ConnectTimeoutSeconds)
			if err != nil {
				return mapError(err)
			}
			continue
		}
		if err != nil {
			return mapError(err)
		}
		if currentRevision != change.ExpectedRevision || currentDomain != route.DomainID || currentSource != model.SourceAPI {
			return ErrPrecondition
		}
		if change.Delete {
			result, e := tx.ExecContext(ctx, s.bind(`UPDATE routes SET enabled=:enabled,route_status=:route_status,revision=revision+1,updated_at=:updated_at WHERE id=:id AND revision=:revision`), false, model.RouteDisabled, time.Now().UTC(), route.ID, currentRevision)
			if e != nil {
				return e
			}
			if e = requireAffected(result, true); e != nil {
				return e
			}
			continue
		}
		result, e := tx.ExecContext(ctx, s.bind(`UPDATE routes SET service_id=:service_id,path=:path,priority=:priority,strip_path=:strip_path,target_port=:target_port,upstream_protocol=:upstream_protocol,upstream_server_name=:upstream_server_name,websocket_enabled=:websocket_enabled,enabled=:enabled,route_status=:route_status,last_error=:last_error,revision=revision+1,updated_at=:updated_at,kind=:kind,match_kind=:match_kind,external_url=:external_url,response_code=:response_code,response_value=:response_value,read_timeout_seconds=:read_timeout_seconds,connect_timeout_seconds=:connect_timeout_seconds WHERE id=:id AND revision=:revision`),
			nullString(route.ServiceID), route.Path, route.Priority, route.StripPath, route.TargetPort, route.UpstreamProtocol, route.UpstreamServerName,
			route.WebsocketEnabled, route.Enabled, route.Status, route.LastError, time.Now().UTC(), route.Kind, route.Match,
			route.ExternalURL, route.ResponseCode, route.ResponseValue, route.ReadTimeoutSeconds, route.ConnectTimeoutSeconds, route.ID, currentRevision)
		if e != nil {
			return mapError(e)
		}
		if e = requireAffected(result, true); e != nil {
			return e
		}
	}
	return nil
}
