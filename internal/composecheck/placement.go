package composecheck

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

type Node struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Role         string            `json:"role"`
	State        string            `json:"state"`
	Availability string            `json:"availability"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	IP           string            `json:"ip"`
	Labels       map[string]string `json:"labels"`
	EngineLabels map[string]string `json:"engine_labels"`
	Version      uint64            `json:"version"`
}

type constraint struct{ key, op, value string }

var expression = regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_.-]*)\s*(==|!=)\s*([^=<>!\r\n]+?)\s*$`)

func parseConstraint(raw string) (constraint, error) {
	m := expression.FindStringSubmatch(raw)
	if len(m) != 4 {
		return constraint{}, fmt.Errorf("invalid placement constraint")
	}
	c := constraint{m[1], m[2], strings.TrimSpace(m[3])}
	k := strings.ToLower(c.key)
	switch k {
	case "node.id", "node.hostname", "node.role", "node.platform.os", "node.platform.arch":
	case "node.ip":
		if _, err := netip.ParseAddr(c.value); err != nil {
			if _, err = netip.ParsePrefix(c.value); err != nil {
				return c, err
			}
		}
	default:
		if !(strings.HasPrefix(k, "node.labels.") && len(k) > 12) && !(strings.HasPrefix(k, "engine.labels.") && len(k) > 14) {
			return c, fmt.Errorf("unsupported placement key")
		}
	}
	return c, nil
}

// Matching follows Swarm's conjunction and case-insensitive value comparison;
// label names remain case sensitive. A preference is never a constraint.
func MatchConstraints(raw []string, nodes []Node) ([]Node, string) {
	constraints := make([]constraint, 0, len(raw))
	for _, s := range raw {
		c, err := parseConstraint(s)
		if err != nil {
			return nil, "PLACEMENT_CONSTRAINT_INVALID"
		}
		constraints = append(constraints, c)
	}
	for i, a := range constraints {
		for _, b := range constraints[i+1:] {
			// IP ranges can overlap without being textually equal.
			if !strings.EqualFold(a.key, "node.ip") && a.key == b.key && ((a.op == "==" && b.op == "==" && !strings.EqualFold(a.value, b.value)) || (a.op != b.op && strings.EqualFold(a.value, b.value))) {
				return nil, "PLACEMENT_CONSTRAINT_CONFLICT"
			}
		}
	}
	matched := []Node{}
	for _, n := range nodes {
		if n.State != "ready" || n.Availability != "active" {
			continue
		}
		ok := true
		for _, c := range constraints {
			value := ""
			same := false
			switch k := strings.ToLower(c.key); k {
			case "node.id":
				value = n.ID
			case "node.hostname":
				value = n.Name
			case "node.role":
				value = n.Role
			case "node.platform.os":
				value = n.OS
			case "node.platform.arch":
				value = n.Arch
			case "node.ip":
				ip, e := netip.ParseAddr(n.IP)
				if e == nil {
					if addr, e := netip.ParseAddr(c.value); e == nil {
						same = ip == addr
					} else if prefix, e := netip.ParsePrefix(c.value); e == nil {
						same = prefix.Contains(ip)
					}
				}
			default:
				if strings.HasPrefix(k, "node.labels.") {
					value = n.Labels[c.key[12:]]
				} else {
					value = n.EngineLabels[c.key[14:]]
				}
			}
			if !strings.EqualFold(c.key, "node.ip") {
				same = strings.EqualFold(value, c.value)
			}
			if same != (c.op == "==") {
				ok = false
				break
			}
		}
		if ok {
			matched = append(matched, n)
		}
	}
	return matched, ""
}
