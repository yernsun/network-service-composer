package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

func TestEffectiveRoutedServicesRespectPrecedenceAndExplicitDisable(t *testing.T) {
	columns := []string{"host", "path", "priority", "source", "id", "service_id", "discovered_enabled", "enabled_override"}
	rows := [][]driver.Value{
		{"z.example.test", "/", int64(0), "API", "z", "service-z", true, nil},
		{"a.example.test", "/api/", int64(100), "DISCOVERY", "discovered", "shadowed-discovery", true, nil},
		{"a.example.test", "/api/", int64(1), "API", "api-low", "shadowed-priority", true, nil},
		{"a.example.test", "/api/", int64(2), "API", "api-z", "shadowed-id", true, nil},
		{"a.example.test", "/api/", int64(2), "API", "api-a", "service-api", true, true},
		{"a.example.test", "/", int64(0), "API", "root", "service-root", false, nil},
		{"a.example.test", "/disabled/", int64(0), "API", "disabled", "must-not-route", true, false},
	}
	script := &queryScript{t: t, transactions: true, replies: []*queryReply{{contains: "SELECT d.host,r.path", columns: columns, values: rows}}}
	s := storeWithScript(t, script)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	selected, err := effectiveRoutedServicesTx(ctx, s, tx, "pool")
	want := map[string]struct{}{"service-z": {}, "service-api": {}, "service-root": {}}
	if err != nil || !reflect.DeepEqual(selected, want) {
		t.Fatalf("effective route targets=%v want=%v error=%v", selected, want, err)
	}
}

func TestDiscoverySelectionReadFailuresNeverProducePartialState(t *testing.T) {
	failure := errors.New("discovery selection read interrupted")
	for _, kind := range []string{"effective", "domains", "routes"} {
		for _, stage := range []string{"query", "scan", "iteration"} {
			t.Run(kind+"/"+stage, func(t *testing.T) {
				script := &queryScript{t: t, transactions: true}
				query := "SELECT d.host,r.path"
				if kind == "domains" {
					query = "FROM domains WHERE pool_id="
				}
				if kind == "routes" {
					script.replies = append(script.replies, boundaryEmpty("FROM domains WHERE pool_id=").query)
					query = "FROM routes r JOIN domains d"
				}
				r := &queryReply{contains: query, columns: []string{"invalid"}}
				switch stage {
				case "query":
					r.err = failure
				case "scan":
					r.values = [][]driver.Value{{"wrong column count"}}
				case "iteration":
					r.rowErr = failure
				}
				script.replies = append(script.replies, r)
				s := storeWithScript(t, script)
				ctx := context.Background()
				tx, err := s.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if kind == "effective" {
					selected, e := effectiveRoutedServicesTx(ctx, s, tx, "pool")
					err = e
					if len(selected) != 0 {
						t.Fatal("failed query returned effective services")
					}
				} else {
					selected, e := gatewayDataPlaneStateTx(ctx, s, tx, "pool")
					err = e
					if len(selected) != 0 {
						t.Fatal("failed query returned partial gateway state")
					}
				}
				if err == nil {
					t.Fatal("failed discovery read treated as empty success")
				}
				if stage != "scan" && !errors.Is(err, failure) {
					t.Fatal("original storage failure lost", err)
				}
			})
		}
	}
}
