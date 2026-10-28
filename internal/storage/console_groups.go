package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
)

type ServiceGroup struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	RuntimeID   string `json:"runtime_id"`
	RuntimeName string `json:"runtime_name"`
	RuntimeType string `json:"runtime_type"`
	Count       int    `json:"count"`
}
type ConsoleFilter struct {
	ServiceFilter
	DomainID  string
	ServiceID string
}

func GroupKey(runtimeID, runtimeType, namespace string) string {
	b, _ := json.Marshal([]string{runtimeID, runtimeType, namespace})
	return base64.RawURLEncoding.EncodeToString(b)
}
func ParseGroupKey(key string) ([]string, error) {
	b, err := base64.RawURLEncoding.DecodeString(key)
	var parts []string
	if err != nil || json.Unmarshal(b, &parts) != nil || len(parts) != 3 || parts[0] == "" || (parts[1] != "docker" && parts[1] != "swarm") {
		return nil, fmt.Errorf("invalid group key")
	}
	return parts, nil
}
func consoleWhere(f ConsoleFilter, routes bool) (string, []any, error) {
	q := " WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=s.id)"
	if routes {
		q += " AND NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id=t.id)"
	}
	args := []any{}
	add := func(condition string, value any) { q += " AND " + condition; args = append(args, value) }
	if !routes && f.ExcludeRemoved {
		add("s.status<>:excluded_status", model.ServiceRemoved)
	}
	if f.RuntimeID != "" {
		add("s.runtime_id=:filter_runtime_id", f.RuntimeID)
	}
	if f.ApplicationID != "" {
		add("s.application_id=:application_id", f.ApplicationID)
	}
	if f.Managed != nil {
		add("s.managed=:managed", *f.Managed)
	}
	if f.Status != "" {
		if routes {
			add("t.route_status=:filter_route_status", f.Status)
		} else {
			add("s.status=:filter_status", f.Status)
		}
	}
	if f.Search != "" {
		q += " AND (LOWER(s.name) LIKE :search_name OR LOWER(s.namespace) LIKE :search_namespace OR LOWER(s.service_key) LIKE :search_service_key)"
		p := "%" + strings.ToLower(strings.TrimSpace(f.Search)) + "%"
		args = append(args, p, p, p)
	}
	if f.GroupKey != "" {
		p, err := ParseGroupKey(f.GroupKey)
		if err != nil {
			return "", nil, err
		}
		add("s.runtime_id=:group_runtime_id", p[0])
		add("r.runtime_type=:runtime_type", p[1])
		add("s.namespace=:namespace", p[2])
	}
	if routes && f.DomainID != "" {
		add("t.domain_id=:domain_id", f.DomainID)
	}
	if routes && f.ServiceID != "" {
		add("t.service_id=:service_id", f.ServiceID)
	}
	return q, args, nil
}
func (s *SQLStore) ListServiceGroups(ctx context.Context, resource string, f ConsoleFilter, page Page) ([]ServiceGroup, error) {
	page = page.normalized()
	from := " FROM services s JOIN runtimes r ON r.id=s.runtime_id"
	routes := resource == "routes"
	if routes {
		from += " JOIN routes t ON t.service_id=s.id"
	}
	where, args, err := consoleWhere(f, routes)
	if err != nil {
		return nil, err
	}
	// Page by the grouped tuple, not by individual service IDs.
	if page.After != "" {
		p, err := ParseGroupKey(page.After)
		if err != nil {
			return nil, err
		}
		where += " AND (s.runtime_id>:runtime_id OR (s.runtime_id=:runtime_id_2 AND r.runtime_type>:runtime_type) OR (s.runtime_id=:runtime_id_3 AND r.runtime_type=:runtime_type_2 AND s.namespace>:namespace))"
		args = append(args, p[0], p[0], p[1], p[0], p[1], p[2])
	}
	count := "COUNT(*)"
	if !routes {
		count = "COUNT(CASE WHEN s.status='REMOVED' THEN NULL ELSE 1 END)"
	}
	query := "SELECT s.runtime_id,r.runtime_type,s.namespace,r.name," + count + from + where + " GROUP BY s.runtime_id,r.runtime_type,s.namespace,r.name ORDER BY s.runtime_id,r.runtime_type,s.namespace LIMIT :limit"
	args = append(args, page.Limit)
	rows, err := s.db.QueryContext(ctx, s.bind(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []ServiceGroup{}
	for rows.Next() {
		var v ServiceGroup
		if err = rows.Scan(&v.RuntimeID, &v.RuntimeType, &v.Name, &v.RuntimeName, &v.Count); err != nil {
			return nil, err
		}
		v.Key = GroupKey(v.RuntimeID, v.RuntimeType, v.Name)
		values = append(values, v)
	}
	return values, rows.Err()
}
func (s *SQLStore) ListRoutesFiltered(ctx context.Context, f ConsoleFilter, page Page) ([]model.Route, error) {
	page = page.normalized()
	where, args, err := consoleWhere(f, true)
	if err != nil {
		return nil, err
	}
	where += " AND t.id>:id"
	args = append(args, page.After, page.Limit)
	columns := strings.Split(routeColumns, ",")
	for i, c := range columns {
		columns[i] = "t." + strings.TrimSpace(c)
	}
	rows, err := s.db.QueryContext(ctx, s.bind("SELECT "+strings.Join(columns, ",")+" FROM routes t LEFT JOIN services s ON s.id=t.service_id LEFT JOIN runtimes r ON r.id=s.runtime_id"+where+" ORDER BY t.id LIMIT :limit"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []model.Route{}
	for rows.Next() {
		v, err := scanRoute(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}
func (s *SQLStore) ListEndpointsPage(ctx context.Context, serviceID string, page Page) ([]model.Endpoint, error) {
	page = page.normalized()
	rows, err := s.db.QueryContext(ctx, s.bind("SELECT "+endpointColumns+" FROM service_instances WHERE service_id=:service_id AND id>:id ORDER BY id LIMIT :limit"), serviceID, page.After, page.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []model.Endpoint{}
	for rows.Next() {
		v, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// Bulk summaries for visible route rows avoid per-row API/SQL lookups.
type ConsoleReference struct {
	Environment *model.EnvironmentReference `json:"environment,omitempty"`
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Host        string                      `json:"host,omitempty"`
	Entrypoints []string                    `json:"entrypoints,omitempty"`
	RuntimeID   string                      `json:"runtime_id,omitempty"`
}

func (s *SQLStore) ConsoleReferences(ctx context.Context, kind string, ids []string) (map[string]ConsoleReference, error) {
	values := map[string]ConsoleReference{}
	if len(ids) == 0 {
		return values, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	var query string
	switch kind {
	case "services":
		query = "SELECT id,name,runtime_id FROM services"
	case "domains":
		query = "SELECT id,host,http_enabled,https_enabled FROM domains"
	default:
		return nil, fmt.Errorf("invalid reference kind")
	}
	rows, err := s.db.QueryContext(ctx, s.bind(query+" WHERE id IN ("+namedPlaceholders("id", len(ids))+")"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v ConsoleReference
		if kind == "services" {
			err = rows.Scan(&v.ID, &v.Name, &v.RuntimeID)
		} else {
			var http, https bool
			err = rows.Scan(&v.ID, &v.Host, &http, &https)
			v.Name = v.Host
			if http {
				v.Entrypoints = append(v.Entrypoints, "http")
			}
			if https {
				v.Entrypoints = append(v.Entrypoints, "https")
			}
		}
		if err != nil {
			return nil, err
		}
		values[v.ID] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if kind == "services" {
		refs, err := s.ServiceEnvironments(ctx, ids)
		if err != nil {
			return nil, err
		}
		for key, ref := range refs {
			value := values[key]
			value.Environment = &ref
			values[key] = value
		}
	}
	return values, nil
}
