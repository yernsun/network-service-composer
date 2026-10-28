package nginx

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type Snippet struct {
	Scope      string `json:"scope"`
	ResourceID string `json:"resource_id,omitempty"`
	Content    string `json:"content"`
}
type Directive struct {
	Name string
	Args []string
	Raw  string
	Line int
}
type SnippetError struct {
	Line    int
	Message string
}

func (e *SnippetError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Message) }

var snippetDirectives = map[string]string{
	"client_max_body_size": "http server location", "client_body_buffer_size": "http server location",
	"proxy_connect_timeout": "http server location", "proxy_read_timeout": "http server location", "proxy_send_timeout": "http server location",
	"proxy_buffering": "http server location", "proxy_buffer_size": "http server location", "proxy_buffers": "http server location", "proxy_busy_buffers_size": "http server location", "proxy_request_buffering": "http server location",
	"add_header": "http server location", "gzip": "http server location", "gzip_types": "http server location", "gzip_min_length": "http server location", "gzip_comp_level": "http server location", "gzip_vary": "http server location", "gzip_proxied": "http server location",
	"allow": "server location", "deny": "server location",
}

var nginxTimeValue = regexp.MustCompile(`^(?:[0-9]+(?:ms|[smhdwMy])?)+$`)
var nginxSizeValue = regexp.MustCompile(`^[0-9]+[kKmMgG]?$`)

// ParseSnippet recognizes quoted/escaped tokens and comments, but deliberately
// has no nested-block grammar. This prevents text escaping its owned context.
func ParseSnippet(scope, content string) ([]Directive, error) {
	if scope != "http" && scope != "server" && scope != "location" {
		return nil, &SnippetError{1, "unknown snippet scope"}
	}
	if len(content) > 65536 {
		return nil, &SnippetError{1, "snippet exceeds 64 KiB"}
	}
	result := []Directive{}
	tokens := []string{}
	var token strings.Builder
	var quote rune
	escaped, comment := false, false
	line, start := 1, 1
	active := false
	flush := func() {
		if active {
			tokens = append(tokens, token.String())
			token.Reset()
			active = false
		}
	}
	for _, ch := range content {
		if ch == '\n' {
			line++
		}
		if comment {
			if ch == '\n' {
				comment = false
			}
			continue
		}
		if escaped {
			token.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			token.WriteRune(ch)
			escaped = true
			active = true
			continue
		}
		if quote != 0 {
			token.WriteRune(ch)
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '#' {
			flush()
			comment = true
			continue
		}
		if ch == '\'' || ch == '"' {
			if len(tokens) == 0 && !active {
				start = line
			}
			quote = ch
			token.WriteRune(ch)
			active = true
			continue
		}
		if ch == '{' || ch == '}' || ch == 0 {
			return nil, &SnippetError{line, "blocks and control characters are not allowed"}
		}
		if ch == ';' {
			flush()
			if len(tokens) == 0 {
				return nil, &SnippetError{line, "empty directive"}
			}
			d := Directive{Name: tokens[0], Args: append([]string(nil), tokens[1:]...), Raw: strings.Join(tokens, " ") + ";", Line: start}
			if e := validateDirective(scope, d); e != nil {
				return nil, e
			}
			result = append(result, d)
			tokens = nil
			continue
		}
		if unicode.IsSpace(ch) {
			flush()
			continue
		}
		if unicode.IsControl(ch) {
			return nil, &SnippetError{line, "control characters are not allowed"}
		}
		if len(tokens) == 0 && !active {
			start = line
		}
		token.WriteRune(ch)
		active = true
	}
	flush()
	if quote != 0 || escaped || len(tokens) > 0 {
		return nil, &SnippetError{line, "unterminated directive or quote"}
	}
	seen := map[string]bool{}
	for _, d := range result {
		if d.Name != "allow" && d.Name != "deny" && d.Name != "add_header" {
			if seen[d.Name] {
				return nil, &SnippetError{d.Line, "duplicate directive " + d.Name}
			}
			seen[d.Name] = true
		}
	}
	return result, nil
}
func validateDirective(scope string, d Directive) error {
	fail := func(message string) error { return &SnippetError{d.Line, message} }
	if !strings.Contains(" "+snippetDirectives[d.Name]+" ", " "+scope+" ") {
		return fail("directive is not permitted in this scope: " + d.Name)
	}
	if len(d.Args) == 0 {
		return fail("directive requires arguments")
	}
	for _, arg := range d.Args {
		if strings.Contains(strings.ToLower(arg), "$nsc_") || strings.Contains(strings.ToLower(arg), "${nsc_") {
			return fail("NSC variables are reserved")
		}
	}
	if d.Name == "add_header" {
		if len(d.Args) < 2 || len(d.Args) > 3 || (len(d.Args) == 3 && d.Args[2] != "always") {
			return fail("add_header requires name, value and optional always")
		}
		name := strings.ToLower(strings.Trim(d.Args[0], `"'`))
		if name == "x-request-id" || strings.HasPrefix(name, "x-nsc-") || strings.ContainsAny(name, "$\\") {
			return fail("reserved or variable header name")
		}
		return nil
	}
	if d.Name == "allow" || d.Name == "deny" {
		if len(d.Args) != 1 {
			return fail("access rule requires one address")
		}
		value := d.Args[0]
		if value != "all" && value != "unix:" && net.ParseIP(value) == nil {
			if _, _, e := net.ParseCIDR(value); e != nil {
				return fail("invalid IP address or CIDR")
			}
		}
		return nil
	}
	if d.Name == "proxy_buffers" {
		if len(d.Args) != 2 {
			return fail("proxy_buffers requires count and size")
		}
		if n, e := strconv.Atoi(d.Args[0]); e != nil || n < 2 {
			return fail("buffer count must be at least two")
		}
		if !nginxSizeValue.MatchString(d.Args[1]) {
			return fail("buffer size must be a valid NGINX size")
		}
		return nil
	}
	if d.Name == "gzip_types" || d.Name == "gzip_proxied" {
		return nil
	}
	if len(d.Args) != 1 {
		return fail("directive requires one argument")
	}
	if strings.HasSuffix(d.Name, "_timeout") && !nginxTimeValue.MatchString(d.Args[0]) {
		return fail("timeout must be a valid NGINX time; omit the directive to use the default")
	}
	if strings.Contains(d.Name, "_size") || d.Name == "gzip_min_length" {
		if !nginxSizeValue.MatchString(d.Args[0]) {
			return fail("size must be a valid NGINX size; omit the directive to use the default")
		}
	}
	if d.Name == "gzip_comp_level" {
		level, err := strconv.Atoi(d.Args[0])
		if err != nil || level < 1 || level > 9 {
			return fail("gzip compression level must be between 1 and 9")
		}
	}
	if d.Name == "gzip" || d.Name == "gzip_vary" || d.Name == "proxy_buffering" || d.Name == "proxy_request_buffering" {
		if d.Args[0] != "on" && d.Args[0] != "off" {
			return fail("expected on or off")
		}
	}
	return nil
}
func normalizedSnippet(scope, content string) (string, error) {
	values, e := ParseSnippet(scope, content)
	if e != nil {
		return "", e
	}
	var result strings.Builder
	for _, d := range values {
		result.WriteString("    " + d.Raw + "\n")
	}
	return result.String(), nil
}

// NGINX normally drops parent add_header directives when a child declares any.
// Merge explicitly so managed X-Request-ID does not hide user HTTP headers.
// A more specific snippet overrides a parent header with the same name.
func inheritSnippetHeaders(child, parent string) string {
	own, _ := ParseSnippet("location", child)
	seen := map[string]bool{}
	for _, d := range own {
		if d.Name == "add_header" {
			seen[strings.ToLower(strings.Trim(d.Args[0], `"'`))] = true
		}
	}
	inherited, _ := ParseSnippet("location", parent)
	for _, d := range inherited {
		if d.Name == "add_header" && !seen[strings.ToLower(strings.Trim(d.Args[0], `"'`))] {
			child += "    " + d.Raw + "\n"
		}
	}
	return child
}
