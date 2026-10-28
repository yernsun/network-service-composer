package execution

import (
	"errors"
	"testing"

	"github.com/yernsun/network-service-composer/internal/storage"
)

func TestStorageExecutionAuthorityRejectsUnsupportedAndWrappedBackends(t *testing.T) {
	f := newExecutionFixture(t)
	for _, backend := range []storage.Store{f.store, struct{ storage.Store }{f.store}} {
		verifier := *f.v
		verifier.Store = backend
		if err := verifier.CheckStorageAuthority(t.Context(), nil, Record{Reference: f.ref}); !errors.Is(err, ErrFenced) {
			t.Fatal("unsupported backend admitted distributed completion", err)
		}
	}
}
