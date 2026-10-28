package core

import (
	"context"
	"time"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func (c *Core) runtimeNotifications(ctx context.Context, since time.Time, revision int64) error {
	page := storage.Page{Limit: 200}
	for {
		runtimes, err := c.store.ListRuntimes(ctx, page)
		if err != nil {
			return err
		}
		for _, runtime := range runtimes {
			status, bad, monitored := c.runtimeCondition(runtime)
			if !monitored {
				continue
			}
			kind := "runtime.problem"
			if !bad {
				kind = "runtime.recovered"
			}
			event := NotificationEvent{ID: since.Format(time.RFC3339Nano), Type: kind, ResourceID: runtime.ID, Status: status}
			if err := c.observeNotification(ctx, event, bad, revision); err != nil {
				return err
			}
		}
		if len(runtimes) < page.Limit {
			return nil
		}
		page.After = runtimes[len(runtimes)-1].ID
	}
}
