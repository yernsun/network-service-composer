package storage

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
)

func dataPlaneBoundaryReplies() []*queryReply {
	columns := make([]string, 27)
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	return []*queryReply{
		{contains: "FROM domains WHERE pool_id", columns: []string{"id", "host", "tls", "redirect", "http", "https", "cert"}, values: [][]driver.Value{{"domain", "app.test", "CUSTOM", true, true, true, "cert"}}},
		{contains: "FROM routes r JOIN domains", columns: columns, values: [][]driver.Value{{"route", "domain", "service", "/", int64(1), false, int64(0), "", "", true, "API", "ACTIVE", "app.test", "CUSTOM", true, "cert", "http", "https", int64(8080), int64(8443), true, true, "runtime", "project/web", "project_web", "project_web", "READY"}}},
	}
}
func TestDataPlaneComparisonRejectsPartialDatabaseEvidence(t *testing.T) {
	for fault := -1; fault < 2; fault++ {
		for _, mode := range []string{"query", "scan", "iteration"} {
			if fault == -1 && mode != "query" {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", fault, mode), func(t *testing.T) {
				failure := errors.New("configuration evidence unavailable")
				replies := dataPlaneBoundaryReplies()
				if fault >= 0 {
					switch mode {
					case "query":
						replies[fault].err = failure
					case "scan":
						replies[fault].values = [][]driver.Value{{nil}}
						replies[fault].columns = []string{"bad"}
					case "iteration":
						replies[fault].rowErr = failure
					}
					replies = replies[:fault+1]
				}
				script := &queryScript{t: t, transactions: true, replies: replies}
				s := storeWithScript(t, script)
				tx, e := s.db.BeginTx(context.Background(), nil)
				if e != nil {
					t.Fatal(e)
				}
				defer tx.Rollback()
				state, e := gatewayDataPlaneStateTx(context.Background(), s, tx, "pool")
				if fault < 0 {
					if e != nil || len(state) != 2 || state[1].protocol != "https" || state[1].targetPort != 8443 || state[1].dnsName != "project_web" {
						t.Fatal("effective overrides absent from comparison", state, e)
					}
				} else {
					if e == nil || state != nil {
						t.Fatal("partial evidence accepted for deciding publication", state, e)
					}
					if mode != "scan" && !errors.Is(e, failure) {
						t.Fatal("read failure identity lost", e)
					}
				}
			})
		}
	}
}
func TestDataPlaneComparisonSeparatesPausedDisabledAndConflictedRoutes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		column int
		value  driver.Value
		route  bool
		paused bool
	}{
		{"missing DNS", 25, "", false, false}, {"DNS collision", 26, "CONFLICT", false, false}, {"disabled", 21, false, false, false}, {"paused", 11, "PAUSED", true, true}, {"missing metadata DNS", 26, "MISSING", true, false}, {"explicit protocol", 7, "http", true, false}, {"explicit port", 6, int64(9000), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := dataPlaneBoundaryReplies()
			replies[1].values[0][tc.column] = tc.value
			script := &queryScript{t: t, transactions: true, replies: replies}
			s := storeWithScript(t, script)
			tx, e := s.db.BeginTx(context.Background(), nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			state, e := gatewayDataPlaneStateTx(context.Background(), s, tx, "pool")
			count := 1
			if tc.route {
				count++
			}
			if e != nil || len(state) != count || state[0].domainID != "domain" {
				t.Fatal("route evidence corrupted domain state", state, e)
			}
			if tc.route && state[1].paused != tc.paused {
				t.Fatal("paused state lost", state)
			}
			if tc.name == "explicit port" && state[1].targetPort != 9000 || tc.name == "explicit protocol" && state[1].protocol != "http" {
				t.Fatal("route override did not take precedence", state)
			}
		})
	}
}
