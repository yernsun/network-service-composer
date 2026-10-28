package nginx

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
)

// Render separate membership maps: geo's longest-prefix selection must never
// let a more-specific allow entry cancel a broader forbidden entry.
func renderIPAccessControls(values []*gatewayv2.IPAccessControl, snippets map[string]string) (string, map[string]string, error) {
	if len(values) > 1000 {
		return "", nil, fmt.Errorf("too many access controls")
	}
	guards := map[string]string{}
	var output strings.Builder
	ordered := append([]*gatewayv2.IPAccessControl(nil), values...)
	for _, value := range ordered {
		if value == nil {
			return "", nil, fmt.Errorf("nil access control")
		}
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Scope+":"+ordered[i].ResourceId < ordered[j].Scope+":"+ordered[j].ResourceId
	})
	for _, acl := range ordered {
		key := acl.Scope + ":" + acl.ResourceId
		if acl.ResourceId == "" || (acl.Scope != "server" && acl.Scope != "location") {
			return "", nil, fmt.Errorf("invalid access control target")
		}
		if _, exists := guards[key]; exists {
			return "", nil, fmt.Errorf("duplicate access control")
		}
		guards[key] = ""
		if len(acl.Access)+len(acl.Forbidden) > 1000 {
			return "", nil, fmt.Errorf("too many IP entries")
		}
		if len(acl.Access)+len(acl.Forbidden) > 0 {
			directives, _ := ParseSnippet(acl.Scope, snippets[key])
			for _, d := range directives {
				if d.Name == "allow" || d.Name == "deny" {
					return "", nil, fmt.Errorf("manual allow/deny conflicts with IP access control")
				}
			}
		}
		sum := sha256.Sum256([]byte(key))
		base := fmt.Sprintf("nsc_ip_%x", sum[:16])
		for _, list := range []struct {
			name                     string
			entries                  []string
			defaultValue, matchValue int
		}{{"access", acl.Access, 0, 1}, {"forbidden", acl.Forbidden, 0, 1}} {
			if len(list.entries) == 0 {
				continue
			}
			fmt.Fprintf(&output, "geo $remote_addr $%s_%s {\n    default %d;\n", base, list.name, list.defaultValue)
			prefixes := map[string]bool{}
			for _, raw := range list.entries {
				parsed, err := ParseIPRange(raw)
				if err != nil {
					return "", nil, err
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
			for _, prefix := range keys {
				fmt.Fprintf(&output, "    %s %d;\n", prefix, list.matchValue)
			}
			output.WriteString("}\n")
		}
		var guard strings.Builder
		if len(acl.Forbidden) > 0 {
			fmt.Fprintf(&guard, "        if ($%s_forbidden = 1) { set $nsc_failure_reason ip_forbidden; return 403; }\n", base)
		}
		if len(acl.Access) > 0 {
			fmt.Fprintf(&guard, "        if ($%s_access = 0) { set $nsc_failure_reason ip_not_allowed; return 403; }\n", base)
		}
		guards[key] = guard.String()
	}
	return output.String(), guards, nil
}

func (r *Renderer) renderHTTPRedirects(output *strings.Builder, domain *domainView) {
	hasRoot := false
	for _, view := range domain.routes {
		modifier := "^~ "
		if view.route.Match == "exact" {
			modifier = "= "
		} else if view.route.Match == "prefix" || view.route.Path == "/" {
			modifier = ""
		}
		if view.route.Path == "/" && view.route.Match != "exact" {
			hasRoot = true
		}
		fmt.Fprintf(output, "    location %s%s {\n", modifier, view.route.Path)
		fmt.Fprintf(output, "        set $nsc_route_id %s;\n", view.route.Id)
		output.WriteString(view.ipGuard)
		output.WriteString("        return 308 https://$host$request_uri;\n    }\n")
	}
	if !hasRoot {
		output.WriteString("    location / {\n" + domain.ipGuard + "        return 308 https://$host$request_uri;\n    }\n")
	}
}
