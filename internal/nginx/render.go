package nginx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	gatewayv2 "github.com/yernsun/network-service-composer/api/gen/nsc/gateway/v2"
	"github.com/yernsun/network-service-composer/internal/config"
)

const RendererVersion = "dns-fragments-upstream-tls-v10"

type Renderer struct {
	config         config.NGINXConfig
	trace          bool
	currentFile    string
	ranges         []SourceRange
	sandboxFixture bool
}

func NewRenderer(cfg config.NGINXConfig) *Renderer { return &Renderer{config: cfg} }

// RenderSandboxFixture is reserved for the locally constructed self-check.
// Normal snapshots still reject arbitrary IP upstreams. The fixed identity and
// address cannot be supplied through the public validation protocol as a bypass.
func (r *Renderer) RenderSandboxFixture(snapshot *gatewayv2.Snapshot, directory string) error {
	fixture := *r
	fixture.sandboxFixture = true
	return fixture.Render(snapshot, directory)
}

type routeView struct {
	ipGuard      string
	route        *gatewayv2.SnapshotRoute
	upstreamName string
	service      *gatewayv2.SnapshotService
	snippet      string
	origins      []directiveOrigin
}

type domainView struct {
	clientIP      string
	ipGuard       string
	id            string
	host          string
	tlsMode       string
	httpEnabled   bool
	httpsEnabled  bool
	http3Enabled  bool
	quicListener  bool
	redirect      bool
	certificateID string
	certificate   *gatewayv2.SnapshotCertificate
	snippet       string
	origins       []directiveOrigin
	httpChallenge bool
	generation    int64
	routes        []routeView
}

type upstreamView struct {
	name       string
	dnsName    string
	protocol   string
	port       int32
	serverName string
}

// MainConfig is independent from every Domain, Route, Service, and
// Certificate. Adding a host never rewrites this file.
func (r *Renderer) MainConfig(pidPath string) []byte {
	var output strings.Builder
	fmt.Fprintf(&output, "worker_processes %s;\n", safeWorkerProcesses(r.config.WorkerProcesses))
	fmt.Fprintf(&output, "pid %s;\n", nginxPath(pidPath))
	output.WriteString("error_log stderr info;\nevents { worker_connections 4096; }\nhttp {\n")
	output.WriteString("  map $http_upgrade $nsc_connection_upgrade { default upgrade; '' close; }\n")
	output.WriteString("  map $http_x_request_id $nsc_request_id { default $http_x_request_id; '' $request_id; }\n")
	// The bootstrap generation intentionally has no Domain fragments. Register
	// every observability variable in the fixed configuration so NGINX can
	// start before the first Snapshot; Domain/Route contexts overwrite these
	// request-local defaults with `set`.
	for _, variable := range []string{"domain_id", "route_id", "service_id", "dns_name", "upstream_name", "generation", "failure_reason"} {
		fmt.Fprintf(&output, "  map $host $nsc_%s { default ''; }\n", variable)
	}
	output.WriteString("  log_format nsc_json escape=json '{\"request_id\":\"$nsc_request_id\",\"host\":\"$host\",\"path\":\"$uri\",\"status\":$status,\"domain_id\":\"$nsc_domain_id\",\"route_id\":\"$nsc_route_id\",\"service_id\":\"$nsc_service_id\",\"dns_name\":\"$nsc_dns_name\",\"upstream\":\"$nsc_upstream_name\",\"upstream_addr\":\"$upstream_addr\",\"upstream_status\":\"$upstream_status\",\"request_time\":$request_time,\"upstream_connect_time\":\"$upstream_connect_time\",\"upstream_response_time\":\"$upstream_response_time\",\"generation\":\"$nsc_generation\",\"failure_reason\":\"$nsc_failure_reason\"}';\n")
	output.WriteString("  access_log /dev/stdout nsc_json;\n  server_tokens off;\n")
	output.WriteString("  resolver 127.0.0.11 valid=5s ipv6=off;\n  resolver_timeout 2s;\n")
	output.WriteString("  include custom/*.conf;\n")
	output.WriteString("  include upstreams/*.conf;\n  include domains/*.conf;\n  include system/*.conf;\n}\n")
	return []byte(output.String())
}

func (r *Renderer) WriteMainConfig(path, pidPath string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeFile(path, r.MainConfig(pidPath), 0o640)
}

// Render writes a complete immutable fragment generation. It never renders a
// container address, task address, or Docker VIP.
func (r *Renderer) Render(snapshot *gatewayv2.Snapshot, directory string) error {
	if snapshot == nil {
		return fmt.Errorf("snapshot is nil")
	}
	for _, name := range []string{"certificates", "upstreams", "domains", "system", "custom"} {
		mode := os.FileMode(0o750)
		if name == "certificates" {
			mode = 0o700
		}
		if err := os.MkdirAll(filepath.Join(directory, name), mode); err != nil {
			return fmt.Errorf("create %s fragment directory: %w", name, err)
		}
	}
	snippets := map[string]string{}
	origins := map[string][]directiveOrigin{}
	for _, v := range snapshot.Snippets {
		if v == nil {
			return fmt.Errorf("nil snippet")
		}
		key := v.Scope + ":" + v.ResourceId
		if _, ok := snippets[key]; ok {
			return fmt.Errorf("duplicate snippet")
		}
		content, err := normalizedSnippet(v.Scope, v.Content)
		if err != nil {
			return err
		}
		snippets[key] = content
		if r.trace {
			origins[key] = snippetOrigins(v.Scope, v.ResourceId, v.Content)
		}
	}
	ipConfig, ipGuards, err := renderIPAccessControls(snapshot.AccessControls, snippets)
	if err != nil {
		return err
	}
	clientIPs, err := renderClientIPSources(snapshot.AccessControls)
	if err != nil {
		return err
	}
	global := snippets["http:"]
	defaults := map[string]string{"proxy_connect_timeout": "5s", "proxy_read_timeout": "60s", "proxy_send_timeout": "60s"}
	directives, _ := ParseSnippet("http", global)
	for _, d := range directives {
		delete(defaults, d.Name)
	}
	for _, name := range []string{"proxy_connect_timeout", "proxy_read_timeout", "proxy_send_timeout"} {
		if value, ok := defaults[name]; ok {
			global += name + " " + value + ";\n"
		}
	}
	if r.trace {
		r.currentFile = "custom/http.conf"
		var trace strings.Builder
		r.emitSnippet(&trace, global, origins["http:"])
	}
	if err := writeFile(filepath.Join(directory, "custom", "http.conf"), []byte(global+ipConfig), 0o640); err != nil {
		return err
	}
	challenge, err := validateUpstreamURL(snapshot.ChallengeUpstream)
	if err != nil {
		return err
	}

	services := make(map[string]*gatewayv2.SnapshotService, len(snapshot.Services))
	for _, service := range snapshot.Services {
		localFixture := r.sandboxFixture && service != nil && service.Id == "svc_sandbox" && service.QualifiedName == "nsc_sandbox_fixture" && service.DnsName == "127.0.0.1"
		if service == nil || !safeID(service.Id) || !localFixture && validateDockerDNSName(service.DnsName) != nil || service.QualifiedName == "" {
			return fmt.Errorf("snapshot contains an invalid DNS service")
		}
		if _, exists := services[service.Id]; exists {
			return fmt.Errorf("snapshot contains duplicate service %s", service.Id)
		}
		services[service.Id] = service
	}

	certificates := make(map[string]*gatewayv2.SnapshotCertificate, len(snapshot.Certificates))
	for _, certificate := range snapshot.Certificates {
		if certificate == nil || !safeID(certificate.Id) {
			return fmt.Errorf("snapshot contains an invalid certificate ID")
		}
		if _, exists := certificates[certificate.Id]; exists {
			return fmt.Errorf("snapshot contains duplicate certificate %s", certificate.Id)
		}
		certificates[certificate.Id] = certificate
		if err := writeFile(filepath.Join(directory, "certificates", certificate.Id+".crt"), []byte(certificate.CertificatePem), 0o600); err != nil {
			return fmt.Errorf("write certificate %s: %w", certificate.Id, err)
		}
		if err := writeFile(filepath.Join(directory, "certificates", certificate.Id+".key"), []byte(certificate.PrivateKeyPem), 0o600); err != nil {
			return fmt.Errorf("write private key %s: %w", certificate.Id, err)
		}
	}

	quicListener := false
	domains := make(map[string]*domainView, len(snapshot.Domains))
	for _, value := range snapshot.Domains {
		if value == nil || !safeID(value.Id) {
			return fmt.Errorf("snapshot contains an invalid domain ID")
		}
		if err := validateHost(value.Host); err != nil {
			return fmt.Errorf("domain %s: %w", value.Id, err)
		}
		if _, exists := domains[value.Host]; exists {
			return fmt.Errorf("snapshot contains duplicate host %s", value.Host)
		}
		if !value.HttpEnabled && !value.HttpsEnabled {
			return fmt.Errorf("domain %s has no entrypoint", value.Id)
		}
		if value.HttpsEnabled && value.TlsMode != "managed" && value.TlsMode != "custom" {
			return fmt.Errorf("domain %s enables HTTPS without TLS", value.Id)
		}
		if value.Http3Enabled && !value.HttpsEnabled {
			return fmt.Errorf("domain %s HTTP/3 requires HTTPS", value.Id)
		}
		if value.Http3Enabled && certificates[value.CertificateId] != nil {
			quicListener = true
		}
		if value.RedirectHttpToHttps && (!value.HttpEnabled || !value.HttpsEnabled) {
			return fmt.Errorf("domain %s redirect requires HTTP and HTTPS", value.Id)
		}
		domains[value.Host] = &domainView{id: value.Id, host: value.Host, tlsMode: value.TlsMode,
			httpEnabled: value.HttpEnabled, httpsEnabled: value.HttpsEnabled, http3Enabled: value.Http3Enabled, redirect: value.RedirectHttpToHttps,
			certificateID: value.CertificateId, certificate: certificates[value.CertificateId],
			httpChallenge: value.TlsMode == "managed" && value.AcmeChallenge == "http-01", generation: snapshot.ConfigRevision}
	}

	for _, domain := range domains {
		domain.quicListener = quicListener
	}
	upstreams := make(map[string]upstreamView)
	for _, route := range snapshot.Routes {
		const acmeChallengePath = "/.well-known/acme-challenge/"
		if route != nil && route.Kind == "" {
			route.Kind = "proxy"
		}
		if route != nil && route.Match == "" {
			route.Match = "prefer"
		}
		if route == nil || !safeID(route.Id) || route.Kind != "proxy" && route.Kind != "return" && route.Kind != "redirect" {
			return fmt.Errorf("snapshot contains an invalid route identity")
		}
		internalProxy := route.Kind == "proxy" && route.ExternalUrl == ""
		if internalProxy && (!safeID(route.ServiceId) || !safeUpstreamName(route.UpstreamName)) {
			return fmt.Errorf("route %s has an invalid service identity", route.Id)
		}
		if route.Kind == "proxy" && route.ExternalUrl != "" {
			if err := ValidateExternalProxyURL(route.ExternalUrl); err != nil {
				return fmt.Errorf("route %s external URL: %w", route.Id, err)
			}
		}
		if route.Kind == "return" || route.Kind == "redirect" {
			if err := ValidateRouteResponse(route.Kind, int(route.ResponseCode), route.ResponseValue); err != nil {
				return fmt.Errorf("route %s response: %w", route.Id, err)
			}
		}
		if err := validateHost(route.Host); err != nil {
			return fmt.Errorf("route %s: %w", route.Id, err)
		}
		if err := validatePath(route.Path); err != nil {
			return fmt.Errorf("route %s: %w", route.Id, err)
		}
		if strings.HasPrefix(route.Path, acmeChallengePath) {
			continue
		}
		protocol := strings.ToLower(strings.TrimSpace(route.UpstreamProtocol))
		if route.Kind == "proxy" && protocol != "http" && protocol != "https" {
			return fmt.Errorf("route %s has an invalid upstream protocol", route.Id)
		}
		if internalProxy && (route.TargetPort < 1 || route.TargetPort > 65535) {
			return fmt.Errorf("route %s has an invalid target port", route.Id)
		}
		if route.UpstreamServerName != "" {
			if err := validateHost(route.UpstreamServerName); err != nil {
				return fmt.Errorf("route %s upstream server name: %w", route.Id, err)
			}
		}
		service := services[route.ServiceId]
		if internalProxy && service == nil {
			return fmt.Errorf("route %s references missing service %s", route.Id, route.ServiceId)
		}
		domain := domains[route.Host]
		if domain == nil {
			return fmt.Errorf("route %s references missing domain %s", route.Id, route.Host)
		}
		candidate := upstreamView{name: route.UpstreamName, protocol: protocol, port: route.TargetPort, serverName: route.UpstreamServerName}
		if service != nil {
			candidate.dnsName = service.DnsName
		}
		if route.Paused {
			domain.routes = append(domain.routes, routeView{route: route, upstreamName: candidate.name, service: service})
			continue
		}
		if internalProxy {
			if existing, exists := upstreams[candidate.name]; exists && existing != candidate {
				return fmt.Errorf("upstream name collision for %s", candidate.name)
			}
			upstreams[candidate.name] = candidate
		}
		domain.routes = append(domain.routes, routeView{route: route, upstreamName: candidate.name, service: service})
	}

	upstreamNames := make([]string, 0, len(upstreams))
	for name := range upstreams {
		upstreamNames = append(upstreamNames, name)
	}
	sort.Strings(upstreamNames)
	for _, name := range upstreamNames {
		value := upstreams[name]
		var output strings.Builder
		fmt.Fprintf(&output, "upstream %s {\n", value.name)
		fmt.Fprintf(&output, "    zone %s 64k;\n", value.name)
		resolve := " resolve"
		if r.sandboxFixture && value.dnsName == "127.0.0.1" {
			resolve = ""
		}
		fmt.Fprintf(&output, "    server %s:%d%s;\n", value.dnsName, value.port, resolve)
		output.WriteString("    keepalive 32;\n}\n")
		if err := writeFile(filepath.Join(directory, "upstreams", value.name+".conf"), []byte(output.String()), 0o640); err != nil {
			return err
		}
	}

	hosts := make([]string, 0, len(domains))
	for host := range domains {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	for _, host := range hosts {
		domain := domains[host]
		domain.clientIP = clientIPs[domain.id]
		domain.ipGuard = ipGuards["server:"+domain.id]
		domain.snippet = inheritSnippetHeaders(snippets["server:"+domain.id], global)
		domain.origins = inheritOrigins(origins["server:"+domain.id], origins["http:"])
		for i := range domain.routes {
			domain.routes[i].ipGuard = domain.ipGuard + ipGuards["location:"+domain.routes[i].route.Id]
			domain.routes[i].snippet = inheritSnippetHeaders(snippets["location:"+domain.routes[i].route.Id], domain.snippet)
			domain.routes[i].origins = inheritOrigins(origins["location:"+domain.routes[i].route.Id], domain.origins)
		}
		if domain.http3Enabled {
			contents := []string{domain.snippet}
			for _, route := range domain.routes {
				contents = append(contents, route.snippet)
			}
			for _, content := range contents {
				directives, _ := ParseSnippet("server", content)
				for _, d := range directives {
					if d.Name == "add_header" && len(d.Args) > 0 && strings.EqualFold(d.Args[0], "Alt-Svc") {
						return fmt.Errorf("domain %s: Alt-Svc is managed when HTTP/3 is enabled", domain.id)
					}
				}
			}
		}
		sort.SliceStable(domain.routes, func(i, j int) bool {
			if len(domain.routes[i].route.Path) != len(domain.routes[j].route.Path) {
				return len(domain.routes[i].route.Path) > len(domain.routes[j].route.Path)
			}
			return domain.routes[i].route.Id < domain.routes[j].route.Id
		})
		r.currentFile = "domains/" + DomainFileName(host)
		content, renderErr := r.renderDomain(domain, challenge)
		if renderErr != nil {
			return renderErr
		}
		if err := writeFile(filepath.Join(directory, "domains", DomainFileName(host)), content, 0o640); err != nil {
			return err
		}
	}
	return writeFile(filepath.Join(directory, "system", "default.conf"), r.defaultServersWithQUIC(snapshot.ConfigRevision, quicListener), 0o640)
}

func (r *Renderer) renderDomain(domain *domainView, challenge string) ([]byte, error) {
	var output strings.Builder
	if domain.httpEnabled || domain.httpChallenge {
		fmt.Fprintf(&output, "server {\n    listen 80;\n    server_name %s;\n", domain.host)
		r.renderServerContext(&output, domain)
		if domain.httpChallenge {
			r.renderChallenge(&output, challenge)
		}
		if !domain.httpEnabled {
			output.WriteString("    location / { return 404; }\n")
		} else if domain.redirect {
			r.renderHTTPRedirects(&output, domain)
		} else {
			r.renderRoutes(&output, domain.routes, domain.ipGuard)
		}
		output.WriteString("}\n")
	}
	if domain.httpsEnabled {
		if domain.certificate == nil {
			if domain.tlsMode == "custom" {
				return nil, fmt.Errorf("domain %s is missing its custom certificate", domain.id)
			}
			return []byte(output.String()), nil
		}
		fmt.Fprintf(&output, "server {\n    listen 443 ssl;\n    http2 on;\n    server_name %s;\n", domain.host)
		if domain.quicListener {
			output.WriteString("    listen 443 quic;\n    ssl_early_data off;\n")
			if domain.http3Enabled {
				output.WriteString("    http3 on;\n")
			} else {
				output.WriteString("    http3 off;\n")
			}
		}
		r.renderServerContext(&output, domain)
		if domain.http3Enabled {
			r.renderAltSvc(&output)
		}
		fmt.Fprintf(&output, "    ssl_certificate certificates/%s.crt;\n    ssl_certificate_key certificates/%s.key;\n", domain.certificate.Id, domain.certificate.Id)
		output.WriteString("    ssl_protocols TLSv1.2 TLSv1.3;\n")
		r.renderHTTPSRoutes(&output, domain.routes, domain.http3Enabled, domain.ipGuard)
		output.WriteString("}\n")
	}
	return []byte(output.String()), nil
}

func (r *Renderer) renderServerContext(output *strings.Builder, domain *domainView) {
	output.WriteString(domain.clientIP)
	fmt.Fprintf(output, "    set $nsc_domain_id %s;\n", domain.id)
	output.WriteString("    set $nsc_route_id '';\n    set $nsc_service_id '';\n    set $nsc_dns_name '';\n    set $nsc_upstream_name '';\n    set $nsc_failure_reason '';\n")
	fmt.Fprintf(output, "    set $nsc_generation %d;\n", domain.generation)
	output.WriteString("    add_header X-Request-ID $nsc_request_id always;\n")
	r.emitSnippet(output, domain.snippet, domain.origins)
}

func (r *Renderer) renderChallenge(output *strings.Builder, challenge string) {
	output.WriteString("    location ^~ /.well-known/acme-challenge/ {\n        allow all;\n")
	fmt.Fprintf(output, "        set $nsc_challenge_upstream %s;\n", challenge)
	output.WriteString("        proxy_pass $nsc_challenge_upstream;\n        proxy_set_header Host $host;\n        proxy_set_header X-Forwarded-Proto $scheme;\n    }\n")
}

func (r *Renderer) renderRoutes(output *strings.Builder, routes []routeView, ipGuard string) {
	r.renderHTTPSRoutes(output, routes, false, ipGuard)
}
func (r *Renderer) renderHTTPSRoutes(output *strings.Builder, routes []routeView, http3 bool, ipGuard string) {
	hasRoot := false
	for _, view := range routes {
		route := view.route
		modifier := "^~ "
		if route.Match == "exact" {
			modifier = "= "
		} else if route.Match == "prefix" {
			modifier = ""
		}
		if route.Path == "/" {
			if route.Match != "exact" {
				modifier = ""
				hasRoot = true
			}
		}
		fmt.Fprintf(output, "    location %s%s {\n", modifier, route.Path)
		fmt.Fprintf(output, "        set $nsc_route_id %s;\n", route.Id)
		if route.ServiceId != "" {
			fmt.Fprintf(output, "        set $nsc_service_id %s;\n", route.ServiceId)
		}
		if view.service != nil {
			fmt.Fprintf(output, "        set $nsc_dns_name %s;\n        set $nsc_upstream_name %s;\n", view.service.DnsName, view.upstreamName)
		}
		output.WriteString(view.ipGuard)
		if route.Paused {
			output.WriteString("        set $nsc_failure_reason route_paused;\n        return 503;\n    }\n")
			continue
		}
		if route.Kind == "return" || route.Kind == "redirect" {
			if view.snippet != "" {
				r.emitSnippet(output, view.snippet, view.origins)
			}
			fmt.Fprintf(output, "        default_type text/plain;\n        return %d %s;\n    }\n", route.ResponseCode, nginxQuotedValue(route.ResponseValue))
			continue
		}
		output.WriteString("        set $nsc_forwarded_host $http_host;\n        if ($nsc_forwarded_host = '') { set $nsc_forwarded_host $host; }\n        proxy_http_version 1.1;\n")
		if route.ExternalUrl == "" {
			output.WriteString("        proxy_set_header Host $nsc_forwarded_host;\n")
		}
		output.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n        proxy_set_header X-Forwarded-Proto $scheme;\n")
		if route.ReadTimeoutSeconds > 0 {
			fmt.Fprintf(output, "        proxy_read_timeout %ds;\n", route.ReadTimeoutSeconds)
		}
		if route.ConnectTimeoutSeconds > 0 {
			fmt.Fprintf(output, "        proxy_connect_timeout %ds;\n", route.ConnectTimeoutSeconds)
		}
		if route.WebsocketEnabled {
			output.WriteString("        proxy_set_header Upgrade $http_upgrade;\n        proxy_set_header Connection $nsc_connection_upgrade;\n")
		}
		if route.UpstreamProtocol == "https" && route.ExternalUrl == "" {
			r.renderUpstreamTrust(output)
			output.WriteString("        proxy_ssl_server_name on;\n")
			serverName := route.UpstreamServerName
			if serverName == "" {
				serverName = view.service.DnsName
			}
			fmt.Fprintf(output, "        proxy_ssl_name %s;\n", serverName)
		}
		if route.ExternalUrl != "" {
			u, _ := url.Parse(route.ExternalUrl)
			fmt.Fprintf(output, "        proxy_set_header Host %s;\n", u.Host)
			if u.Scheme == "https" {
				r.renderUpstreamTrust(output)
				fmt.Fprintf(output, "        proxy_ssl_server_name on;\n        proxy_ssl_name %s;\n", u.Hostname())
			}
		}
		if view.snippet != "" {
			r.emitSnippet(output, view.snippet, view.origins)
			directives, _ := ParseSnippet("location", view.snippet)
			for _, d := range directives {
				if d.Name == "add_header" {
					output.WriteString("    add_header X-Request-ID $nsc_request_id always;\n")
					if http3 {
						r.renderAltSvc(output)
					}
					break
				}
			}
		}
		uri := ""
		if route.StripPath {
			uri = "/"
		}
		if route.ExternalUrl != "" {
			fmt.Fprintf(output, "        proxy_pass %s;\n    }\n", route.ExternalUrl)
		} else {
			fmt.Fprintf(output, "        proxy_pass %s://%s%s;\n    }\n", route.UpstreamProtocol, view.upstreamName, uri)
		}
	}
	// Never fall through to NGINX's compiled-in static document root when a
	// Domain has no Route or only path-specific Routes.
	if !hasRoot {
		if ipGuard == "" {
			output.WriteString("    location / { return 404; }\n")
		} else {
			output.WriteString("    location / {\n" + ipGuard + "        return 404;\n    }\n")
		}
	}
}

func (r *Renderer) renderUpstreamTrust(output *strings.Builder) {
	caFile := r.config.UpstreamCAFile
	if caFile == "" {
		caFile = "/etc/ssl/certs/ca-certificates.crt"
	}
	output.WriteString("        proxy_ssl_verify on;\n        proxy_ssl_verify_depth 5;\n")
	fmt.Fprintf(output, "        proxy_ssl_trusted_certificate %s;\n", nginxQuotedValue(caFile))
}

func ValidateExternalProxyURL(raw string) error {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\t ;{}\"'\\$") {
		return fmt.Errorf("invalid external proxy URL")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return fmt.Errorf("invalid external proxy URL")
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid external proxy port")
		}
	}
	return nil
}

func ValidateRouteResponse(kind string, code int, value string) error {
	if len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00$") {
		return fmt.Errorf("invalid response value")
	}
	if kind == "return" {
		if code != 200 && code != 204 && code != 403 && code != 404 && code != 503 {
			return fmt.Errorf("unsupported response status")
		}
		return nil
	}
	if code != 301 && code != 302 && code != 307 && code != 308 {
		return fmt.Errorf("unsupported redirect status")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("redirect target must be an HTTP(S) URL")
	}
	return nil
}

func nginxQuotedValue(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func (r *Renderer) defaultServers(revision int64) []byte {
	// Keep settings introduced by a renderer inside its immutable generation.
	// Rewriting the shared main file would change older generations' digests.
	return []byte(fmt.Sprintf("variables_hash_bucket_size 128;\nvariables_hash_max_size 8192;\nserver {\n    listen 80 default_server;\n    server_name _;\n    set $nsc_generation %d;\n    add_header X-Request-ID $nsc_request_id always;\n    location = /_nsc/generation-health {\n        allow 127.0.0.1;\n        deny all;\n        default_type application/json;\n        add_header Cache-Control no-store always;\n        add_header X-NSC-Config-Revision %d always;\n        return 200 '{\"config_revision\":%d,\"renderer_version\":\"%s\"}';\n    }\n    location / { return 404; }\n}\nserver { listen 443 ssl default_server; server_name _; ssl_reject_handshake on; }\n", revision, revision, revision, RendererVersion))
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(data)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func DomainFileName(host string) string {
	const max = 180
	if len(host)+len(".conf") <= max {
		return host + ".conf"
	}
	digest := sha256.Sum256([]byte(host))
	return host[:max-1-16-len(".conf")] + "-" + hex.EncodeToString(digest[:8]) + ".conf"
}

func validateHost(value string) error {
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil {
		return fmt.Errorf("invalid exact hostname")
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '.' {
			return fmt.Errorf("invalid exact hostname")
		}
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("invalid exact hostname")
		}
	}
	return nil
}

func validateDockerDNSName(value string) error {
	if value == "" || len(value) > 253 || net.ParseIP(value) != nil {
		return fmt.Errorf("invalid Docker service DNS name")
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("invalid Docker service DNS name")
		}
	}
	return nil
}

func validatePath(value string) error {
	if value == "" || !strings.HasPrefix(value, "/") {
		return fmt.Errorf("invalid route path")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`{};"'\\`, r) {
			return fmt.Errorf("invalid route path")
		}
	}
	return nil
}

func validateUpstreamURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(value, " \t\r\n;{}") {
		return "", fmt.Errorf("snapshot contains an invalid ACME challenge upstream")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func safeID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func safeUpstreamName(value string) bool {
	if !strings.HasPrefix(value, "nsc_") || len(value) > 220 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' {
			return false
		}
	}
	return true
}

func safeWorkerProcesses(value string) string {
	if value == "auto" {
		return value
	}
	if number, err := strconv.Atoi(value); err == nil && number > 0 && number <= 1024 {
		return value
	}
	return "auto"
}

func nginxPath(value string) string { return strings.ReplaceAll(value, " ", "\\ ") }

func (r *Renderer) renderAltSvc(output *strings.Builder) {
	port := r.config.HTTP3AdvertisedPort
	if port == 0 {
		port = 443
	}
	fmt.Fprintf(output, "    add_header Alt-Svc 'h3=\":%d\"; ma=300' always;\n", port)
}
func (r *Renderer) defaultServersWithQUIC(revision int64, enabled bool) []byte {
	value := string(r.defaultServers(revision))
	if enabled {
		value = strings.Replace(value, "listen 443 ssl default_server;", "listen 443 ssl default_server; listen 443 quic reuseport default_server; http3 off; ssl_early_data off;", 1)
	}
	return []byte(value)
}
