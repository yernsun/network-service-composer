package nginx

import (
	"errors"
	"strings"
	"testing"
)

func TestSnippetDirectiveValidationMaintainsOwnedContext(t *testing.T) {
	for _, tc := range []struct{ scope, content, want string }{
		{"http", "allow all;", "scope"}, {"server", "gzip;", "arguments"},
		{"location", `add_header X-Test "${NsC_secret}";`, "reserved"},
		{"server", "add_header X-Test;", "requires name"}, {"server", "add_header X-Test value always extra;", "requires name"}, {"server", "add_header X-Test value sometimes;", "requires name"},
		{"server", "add_header X-Request-ID value;", "reserved"}, {"server", "add_header X-Nsc-Internal value;", "reserved"}, {"server", "add_header $header value;", "reserved"}, {"server", `add_header X\Header value;`, "reserved"},
		{"server", "allow all unix:;", "one address"}, {"location", "deny hostname.invalid;", "invalid IP"},
		{"http", "proxy_buffers 2;", "count and size"}, {"http", "proxy_buffers invalid 8k;", "at least two"}, {"http", "proxy_buffers 1 8k;", "at least two"},
		{"http", "proxy_buffers 2 null;", "valid NGINX size"},
		{"location", "client_max_body_size 1m 2m;", "one argument"}, {"location", "gzip maybe;", "on or off"},
		{"location", "client_max_body_size null;", "valid NGINX size"}, {"location", "proxy_connect_timeout nulls;", "valid NGINX time"},
		{"location", "proxy_read_timeout undefined;", "valid NGINX time"}, {"http", "gzip_min_length null;", "valid NGINX size"},
		{"http", "gzip_comp_level 0;", "between 1 and 9"}, {"http", "gzip_comp_level 10;", "between 1 and 9"},
		{"location", "gzip on;\ngzip off;", "duplicate"},
		{"location", "gzip on;\n\x01", "control characters"},
		{"location", "gzip on;\n;", "empty directive"},
		{"location", "gzip on\\", "unterminated"},
	} {
		t.Run(tc.content, func(t *testing.T) {
			ds, e := ParseSnippet(tc.scope, tc.content)
			var se *SnippetError
			if e == nil || !errors.As(e, &se) || !strings.Contains(e.Error(), tc.want) || se.Line < 1 || len(ds) != 0 {
				t.Fatalf("unsafe snippet accepted or unlocatable: %v %v", ds, e)
			}
		})
	}
	for _, tc := range []struct {
		scope, content string
		count          int
	}{
		{"server", "allow all; deny unix:; allow 127.0.0.1; allow ::1; deny 10.0.0.0/8;", 5},
		{"http", "proxy_buffers 2 8k; gzip_types text/plain application/json; gzip_proxied any;", 3},
		{"http", "gzip on; gzip_vary off; proxy_buffering on; proxy_request_buffering off;", 4},
		{"http", "proxy_connect_timeout 250ms; proxy_read_timeout 1m; client_max_body_size 0; gzip_comp_level 6;", 4},
		{"server", `add_header X-Test value; add_header X-Other "value with spaces" always;`, 2},
		{"location", "# first\nadd_header X-Test 'one;two'; # second\nproxy_read_timeout 5s;", 2},
		{"location", "  # empty\n", 0},
	} {
		ds, e := ParseSnippet(tc.scope, tc.content)
		if e != nil || len(ds) != tc.count {
			t.Fatalf("supported snippet rejected: %q %v", tc.content, e)
		}
		for _, d := range ds {
			if !strings.HasSuffix(d.Raw, ";") || d.Line < 1 {
				t.Fatal("source location or directive terminator lost")
			}
		}
	}
}
func TestSnippetNormalizationAndHeaderInheritance(t *testing.T) {
	normalized, e := normalizedSnippet("server", "add_header X-Test value;\n gzip on;")
	if e != nil || normalized != "    add_header X-Test value;\n    gzip on;\n" {
		t.Fatalf("normalization changed content: %q %v", normalized, e)
	}
	if got, e := normalizedSnippet("server", "include /etc/passwd;"); e == nil || got != "" {
		t.Fatal("normalization leaked disallowed configuration")
	}
	child := "add_header X-Test child; gzip on;\n"
	got := inheritSnippetHeaders(child, "add_header x-test parent; proxy_buffering on; add_header X-Other other always;")
	if strings.Contains(got, "parent") || strings.Contains(got, "proxy_buffering") || strings.Count(got, "X-Other") != 1 || !strings.HasPrefix(got, child) {
		t.Fatalf("header inheritance overwrote child or inherited unrelated configuration: %q", got)
	}
}
