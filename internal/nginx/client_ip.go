package nginx

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
)

// Keep names compatible with NGINX's default request header handling and prevent
// configuration injection or selecting the special PROXY protocol input.
var clientIPHeader = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`)

func ValidClientIPHeader(header string) bool { return clientIPHeader.MatchString(header) }

func renderClientIPSources(controls []*gatewayv2.IPAccessControl) (map[string]string, error) {
	result := map[string]string{}
	for _, acl := range controls {
		if acl == nil || acl.ClientIp == nil {
			continue
		}
		source := acl.ClientIp
		if acl.Scope != "server" || !ValidClientIPHeader(source.Header) {
			return nil, fmt.Errorf("invalid Domain client IP source")
		}
		if len(source.TrustedProxies) == 0 || len(source.TrustedProxies) > 1000 {
			return nil, fmt.Errorf("configure between 1 and 1000 trusted proxy IP entries")
		}
		prefixes := map[string]bool{}
		for _, raw := range source.TrustedProxies {
			parsed, err := ParseIPRange(raw)
			if err != nil {
				return nil, err
			}
			for _, prefix := range parsed {
				prefixes[prefix.String()] = true
			}
		}
		keys := make([]string, 0, len(prefixes))
		for prefix := range prefixes {
			keys = append(keys, prefix)
		}
		sort.Strings(keys)
		var output strings.Builder
		for _, prefix := range keys {
			fmt.Fprintf(&output, "    set_real_ip_from %s;\n", prefix)
		}
		fmt.Fprintf(&output, "    real_ip_header %s;\n    real_ip_recursive on;\n", source.Header)
		result[acl.ResourceId] = output.String()
	}
	return result, nil
}
