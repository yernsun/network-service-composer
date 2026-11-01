package main

import (
	"crypto/rand"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"testing"
)

func TestFixtureRegistryCredentialsUseStdinAndBoundedBcrypt(t *testing.T) {
	password := rand.Text()
	result, err := fixtureHash(strings.NewReader(password))
	if err != nil || bcrypt.CompareHashAndPassword(result, []byte(password)) != nil {
		t.Fatal("fixture hash did not authenticate its random input", err)
	}
	for _, value := range []string{"", strings.Repeat("x", 73)} {
		if _, err := fixtureHash(strings.NewReader(value)); err == nil {
			t.Fatal("unbounded fixture input accepted")
		}
	}
}
