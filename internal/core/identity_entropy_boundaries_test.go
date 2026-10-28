package core

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
)

type identityEntropyFailure struct{ err error }

func (r identityEntropyFailure) Read([]byte) (int, error) { return 0, r.err }

func TestPasswordHashEntropyFailurePreservesExistingIdentity(t *testing.T) {
	c, owner := identityBoundaryFixture(t)
	ctx := context.Background()
	before, e := c.store.GetControlDocument(ctx, identityKey)
	if e != nil {
		t.Fatal(e)
	}
	failure := errors.New("password salt entropy unavailable")
	// Existing-account password replacement reaches bcrypt's entropy boundary
	// without ID generation. Restore the process reader before any other work.
	func() {
		original := rand.Reader
		rand.Reader = identityEntropyFailure{failure}
		defer func() { rand.Reader = original }()
		_, e = c.SaveUser(ctx, owner.ID, UserInput{Username: owner.Username, Role: owner.Role, Enabled: true, Password: "replacement-password-123456"}, owner.Revision)
	}()
	if !errors.Is(e, failure) {
		t.Fatal("hash failure hidden", e)
	}
	after, e := c.store.GetControlDocument(ctx, identityKey)
	if e != nil || after.Revision != before.Revision || string(after.Payload) != string(before.Payload) {
		t.Fatal("failed password reset changed identity", e)
	}
	user, e := c.Authenticate(ctx, owner.Username, c.config.Security.AdminToken, "")
	if e != nil || user.ID != owner.ID {
		t.Fatal("old password stopped working after rejected reset", e)
	}
}
