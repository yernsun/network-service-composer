package core

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	applicationx "github.com/yernsun/network-service-composer/internal/application"
	"github.com/yernsun/network-service-composer/internal/composecheck"
	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

type builtinSnapshotRaceStore struct {
	storage.Store
	changed bool
}

func (s builtinSnapshotRaceStore) UpdateCatalogSource(ctx context.Context, v *model.CatalogSource, expected int64) error {
	other := *v
	if s.changed {
		other.MarketJSON = `{"different_snapshot":true}`
	}
	if err := s.Store.UpdateCatalogSource(ctx, &other, expected); err != nil {
		return err
	}
	return storage.ErrPrecondition
}

func TestBuiltinSnapshotRaceAcceptsOnlyIdenticalCommittedDocuments(t *testing.T) {
	for _, changed := range []bool{false, true} {
		c, _, _ := controlFixture(t)
		c.store = builtinSnapshotRaceStore{Store: c.store, changed: changed}
		err := c.EnsureBuiltinCatalog(context.Background())
		if changed && !errors.Is(err, storage.ErrPrecondition) || !changed && err != nil {
			t.Fatal("catalog snapshot race lost immutable evidence boundary", changed, err)
		}
	}
}

func TestConcurrentBuiltinInitializationKeepsOneImmutableCatalog(t *testing.T) {
	c, _, _ := controlFixture(t)
	start := make(chan struct{})
	errors := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			errors <- c.EnsureBuiltinCatalog(context.Background())
		}()
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal("concurrent controller initialization failed", err)
		}
	}
	sources, err := c.listAllCatalogSources(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatal("concurrent initialization duplicated catalog ownership", sources, err)
	}
	versions, err := c.store.ListCatalogAppVersions(context.Background(), sources[0].ID, "", storage.Page{Limit: 100})
	if err != nil || len(versions) != 13 {
		t.Fatal("concurrent initialization lost immutable versions", len(versions), err)
	}
}

func TestCatalogRuntimeSelectionPinsAndVerifiesSwarmArtifact(t *testing.T) {
	c, rt, _ := controlFixture(t)
	ctx := context.Background()
	if err := c.EnsureBuiltinCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	versions, err := c.store.ListCatalogAppVersions(ctx, "", "redis", storage.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var version model.CatalogAppVersion
	for _, v := range versions {
		if v.SwarmYAML != "" {
			version = v
		}
	}
	if version.ID == "" {
		t.Fatal("missing Swarm market version")
	}
	input := ApplicationPreflightInput{RuntimeID: rt.ID, CatalogAppVersionID: version.ID}
	compose, err := c.resolveCatalogInput(ctx, input)
	if err != nil || compose.ComposeYAML != version.ComposeYAML {
		t.Fatal("Compose variant lost", err)
	}
	rt.Type = model.RuntimeSwarm
	c.store = networkRuntimeStore{Store: c.store, runtime: rt}
	if _, err := c.resolveCatalogInput(ctx, input); err == nil {
		t.Fatal("missing data node accepted")
	}
	input.DeploymentPlan = &composecheck.Plan{DataNodeID: "node-one"}
	swarm, err := c.resolveCatalogInput(ctx, input)
	if err != nil || !strings.Contains(swarm.ComposeYAML, "node.id == node-one") || strings.Contains(swarm.ComposeYAML, "restart: unless-stopped") {
		t.Fatal("Swarm variant not selected", err)
	}
	if err := applicationx.ValidateDataNodePolicy(swarm.ComposeYAML, "node-one", nil); err != nil {
		t.Fatal(err)
	}
	base := model.ApplicationRevision{ComposeSHA256: digestString(swarm.ComposeYAML), ConfigurationSource: model.DeploymentSource{DeploymentPlan: *input.DeploymentPlan}}
	if !catalogBaseMatches(version, base) {
		t.Fatal("derived revision cannot inherit catalog metadata")
	}
	for _, scenario := range []string{"template", "digest", "artifact"} {
		t.Run(scenario, func(t *testing.T) {
			corrupt := version
			switch scenario {
			case "template":
				corrupt.SwarmYAML += "#tampered\n"
			case "digest":
				corrupt.SwarmSHA256 = strings.Repeat("0", 64)
			case "artifact":
				corrupt.ArtifactDigest = strings.Repeat("0", 64)
			}
			c.store = &applicationCatalogStore{Store: c.store, version: corrupt}
			if _, err := c.resolveCatalogInput(ctx, input); err == nil {
				t.Fatal("corrupt Swarm artifact accepted")
			}
		})
	}
}
