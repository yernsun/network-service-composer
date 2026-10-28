package nginx

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestIPRangeCIDRCover(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{" 192.168.1.10 ", []string{"192.168.1.10/32"}},
		{"10.1.2.3/8", []string{"10.0.0.0/8"}},
		{"192.168.1.10-192.168.1.20", []string{"192.168.1.10/31", "192.168.1.12/30", "192.168.1.16/30", "192.168.1.20/32"}},
		{"0.0.0.0-255.255.255.255", []string{"0.0.0.0/0"}},
		{"255.255.255.255-255.255.255.255", []string{"255.255.255.255/32"}},
		{"2001:db8::", []string{"2001:db8::/128"}},
		{"2001:db8::1/64", []string{"2001:db8::/64"}},
		{"2001:db8::1-2001:db8::6", []string{"2001:db8::1/128", "2001:db8::2/127", "2001:db8::4/127", "2001:db8::6/128"}},
		{"::-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", []string{"::/0"}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := ParseIPRange(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			strings := []string{}
			for _, p := range got {
				strings = append(strings, p.String())
			}
			if !reflect.DeepEqual(strings, tc.want) {
				t.Fatalf("%v != %v", strings, tc.want)
			}
		})
	}
	for _, raw := range []string{strings.Repeat(" ", 129) + "::1", "", "all", "unix:", "example.test", "127.0.0.1;return 200", "10.0.0.0/33", "::1/129", "fe80::1%eth0", "::ffff:127.0.0.1", "::ffff:127.0.0.1/128", "10.0.0.2-10.0.0.1", "10.0.0.1-::1", "::2-::1", "1.2.3.4-", "1.2.3.4-1.2.3.5-1.2.3.6"} {
		if _, err := ParseIPRange(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestIPRangeExactMembership(t *testing.T) {
	for start := 0; start < 32; start++ {
		for end := start; end < 32; end++ {
			first := netip.AddrFrom4([4]byte{10, 0, 0, byte(start)})
			last := netip.AddrFrom4([4]byte{10, 0, 0, byte(end)})
			prefixes, err := ParseIPRange(first.String() + "-" + last.String())
			if err != nil {
				t.Fatal(err)
			}
			for n := 0; n < 33; n++ {
				address := netip.AddrFrom4([4]byte{10, 0, 0, byte(n)})
				matched := false
				for _, p := range prefixes {
					matched = matched || p.Contains(address)
				}
				if matched != (n >= start && n <= end) {
					t.Fatalf("range %d-%d incorrectly covers %d", start, end, n)
				}
			}
		}
	}
}
