// Package upstream contains the canonical identity rules shared by the
// controller, API diagnostics, and the Gateway renderer.
package upstream

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const maxSafeNameLength = 160

// Name returns the stable NGINX upstream name for a Route. Docker object IDs
// and instance addresses are intentionally absent so recreates and scaling do
// not alter the rendered configuration.
func Name(runtimeID, serviceKey, qualifiedName string, targetPort int, protocol, serverName string) (string, error) {
	qualifiedName = strings.TrimSpace(qualifiedName)
	if qualifiedName == "" {
		return "", fmt.Errorf("qualified service name is required")
	}
	if targetPort < 1 || targetPort > 65535 {
		return "", fmt.Errorf("target port is outside 1..65535")
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "http" && protocol != "https" {
		return "", fmt.Errorf("upstream protocol must be http or https")
	}

	canonical := runtimeID + "\x00" + serviceKey + "\x00" + strconv.Itoa(targetPort) + "\x00" + protocol + "\x00" + strings.TrimSpace(serverName)
	digest := sha256.Sum256([]byte(canonical))
	return "nsc_" + safeName(qualifiedName) + "_" + hex.EncodeToString(digest[:4]), nil
}

func safeName(value string) string {
	var result strings.Builder
	result.Grow(min(len(value), maxSafeNameLength))
	separator := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if result.Len() >= maxSafeNameLength {
				break
			}
			result.WriteRune(r)
			separator = false
			continue
		}
		if !separator && result.Len() > 0 && result.Len() < maxSafeNameLength {
			result.WriteByte('_')
			separator = true
		}
	}
	name := strings.Trim(result.String(), "_")
	if name == "" {
		return "service"
	}
	return name
}
