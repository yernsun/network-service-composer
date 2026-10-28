package nginx

import (
	"fmt"
	"math/big"
	"net/netip"
	"strings"
)

// ParseIPRange returns a bounded, exact CIDR cover without enumerating hosts.
// The caller retains raw for persistence and uses these prefixes for rendering.
func ParseIPRange(raw string) ([]netip.Prefix, error) {
	value := strings.TrimSpace(raw)
	if len(raw) > 128 {
		return nil, fmt.Errorf("IP entry is too long")
	}
	if strings.Contains(value, "/") {
		p, err := netip.ParsePrefix(value)
		if err != nil || p.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid IP prefix")
		}
		return []netip.Prefix{p.Masked()}, nil
	}
	parts := strings.Split(value, "-")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid IP range")
	}
	start, err := netip.ParseAddr(strings.TrimSpace(parts[0]))
	if err != nil || start.Zone() != "" || start.Is4In6() {
		return nil, fmt.Errorf("invalid IP address")
	}
	if len(parts) == 1 {
		return []netip.Prefix{netip.PrefixFrom(start, start.BitLen())}, nil
	}
	end, err := netip.ParseAddr(strings.TrimSpace(parts[1]))
	if err != nil || end.Zone() != "" || end.Is4In6() || start.BitLen() != end.BitLen() || start.Compare(end) > 0 {
		return nil, fmt.Errorf("invalid IP range endpoints")
	}
	width := start.BitLen()
	current := new(big.Int).SetBytes(start.AsSlice())
	last := new(big.Int).SetBytes(end.AsSlice())
	one := big.NewInt(1)
	prefixes := []netip.Prefix{}
	for current.Cmp(last) <= 0 {
		hostBits := 0
		for hostBits < width && current.Bit(hostBits) == 0 {
			hostBits++
		}
		remaining := new(big.Int).Add(new(big.Int).Sub(last, current), one)
		hostBits = min(hostBits, remaining.BitLen()-1)
		bytes := current.FillBytes(make([]byte, width/8))
		address, _ := netip.AddrFromSlice(bytes)
		prefixes = append(prefixes, netip.PrefixFrom(address, width-hostBits))
		current.Add(current, new(big.Int).Lsh(new(big.Int).Set(one), uint(hostBits)))
	}
	return prefixes, nil
}
