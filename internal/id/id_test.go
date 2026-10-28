package id

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestTypedUUIDv7(t *testing.T) {
	for _, kind := range []Kind{Pool, Runtime, Service, Endpoint, Domain, Route, Certificate, Gateway, Challenge, Account, Snapshot, Audit} {
		value := New(kind)
		if !strings.HasPrefix(value, string(kind)+"_") {
			t.Fatalf("%s has the wrong prefix", value)
		}
		if err := Validate(value, kind); err != nil {
			t.Fatalf("Validate(%s): %v", value, err)
		}
		if err := Validate(value, Pool); err == nil && kind != Pool {
			t.Fatalf("%s was accepted as a pool ID", value)
		}
	}
}

func TestValidateRejectsNonV7UUID(t *testing.T) {
	value := string(Gateway) + "_" + uuid.NewString()
	if err := Validate(value, Gateway); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("UUIDv4 returned %v", err)
	}
}
