package storage

import "context"

// ResourceCounts uses database aggregates rather than fetching resource pages.
func (s *SQLStore) ResourceCounts(ctx context.Context) (map[string]int64, error) {
	counts := map[string]int64{}
	for _, item := range []struct{ key, table string }{{"pools", "gateway_pools"}, {"runtimes", "runtimes"}, {"services", "services"}, {"domains", "domains"}, {"gateways", "gateways"}, {"certificates", "certificates"}} {
		var count int64
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+item.table+" WHERE NOT EXISTS (SELECT 1 FROM control_documents tomb WHERE tomb.kind='deleted-project-guard' AND tomb.parent_id="+item.table+".id)").Scan(&count); err != nil {
			return nil, err
		}
		counts[item.key] = count
	}
	return counts, nil
}
