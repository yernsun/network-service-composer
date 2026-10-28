package core

import (
	"context"
	"slices"

	"github.com/yernsun/network-service-composer/internal/storage"
)

type IssueFilter struct{ State, ResourceType, ResourceID, EnvironmentID, ProjectID, Code string }

// Filter the durable ownership snapshot, never query-supplied ownership claims.
func (c *Core) IssuesFiltered(ctx context.Context, page storage.Page, filter IssueFilter) ([]Issue, error) {
	if filter.State != "" && filter.State != "all" && filter.State != "active" && filter.State != "resolved" {
		return nil, controlInvalid("state", "select active, resolved or all")
	}
	result := []Issue{}
	limit := page.Limit
	if limit < 1 || limit > 200 {
		limit = 200
	}
	for scan := (storage.Page{After: page.After, Limit: 200}); ; {
		rows, err := c.Issues(ctx, scan)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if filter.State == "active" && !row.Active || filter.State == "resolved" && row.Active || filter.ResourceType != "" && row.ResourceType != filter.ResourceType || filter.ResourceID != "" && row.ResourceID != filter.ResourceID || filter.Code != "" && row.Code != filter.Code {
				continue
			}
			if (filter.ProjectID != "" || filter.EnvironmentID != "") && !slices.ContainsFunc(row.Scopes, func(scope AlertScope) bool {
				return (filter.ProjectID == "" || scope.ProjectID == filter.ProjectID) && (filter.EnvironmentID == "" || scope.EnvironmentID == filter.EnvironmentID)
			}) {
				continue
			}
			result = append(result, row)
			if len(result) == limit {
				return result, nil
			}
		}
		if len(rows) < scan.Limit {
			return result, nil
		}
		scan.After = rows[len(rows)-1].ID
	}
}
