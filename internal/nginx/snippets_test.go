package nginx

import (
	"strings"
	"testing"
)

func TestSnippetGrammarAndManagedBoundaries(t *testing.T) {
	valid := []string{`# a comment
add_header X-Test "a; b # c" always; proxy_read_timeout 2s;`, `client_max_body_size 20m; gzip on;`, `allow 10.0.0.0/8; deny all;`}
	for _, value := range valid {
		if _, e := ParseSnippet("server", value); e != nil {
			t.Fatal(value, e)
		}
	}
	for _, value := range []string{`}; server { listen 9000; }`, `include /etc/passwd;`, `proxy_pass http://elsewhere;`, `add_header X-NSC-Config-Revision nope;`, `add_header X-Request-ID nope;`, `add_header X-Test $nsc_generation;`, `gzip on; gzip off;`, `gzip on`, `add_header X-Test "unterminated;`, strings.Repeat("#", 65537)} {
		if _, e := ParseSnippet("server", value); e == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if _, e := ParseSnippet("http", "allow all;"); e == nil {
		t.Fatal("http access rule accepted")
	}
}
