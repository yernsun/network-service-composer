// bootstrapfixtureauth creates a disposable registry password hash. Credentials
// arrive only on stdin; this helper is never part of an NSC runtime image.
package main

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"os"
)

func fixtureHash(input io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(input, 73))
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if len(data) < 16 || len(data) > 72 {
		return nil, fmt.Errorf("fixture credential requires 16 to 72 bytes")
	}
	// This low cost is restricted to random, disposable test registry credentials.
	return bcrypt.GenerateFromPassword(data, bcrypt.MinCost)
}
func main() {
	hash, err := fixtureHash(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture credential hashing failed")
		os.Exit(1)
	}
	fmt.Println(string(hash))
}
