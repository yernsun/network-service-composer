package runtime

import (
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"golang.org/x/net/idna"

	"github.com/yernsun/network-service-composer/internal/model"
)

type LabelConfig struct {
	Enabled   bool
	Port      int
	Protocol  string
	Readiness model.ReadinessPolicy
	Routes    []model.DiscoveredRoute
}

func ParseLabels(labels map[string]string, defaults model.ReadinessPolicy) (LabelConfig, error) {
	value := LabelConfig{Readiness: defaults, Protocol: "http"}
	enabled, err := parseBool(labels, "nsc.enable", false)
	if err != nil {
		return value, err
	}
	value.Enabled = enabled
	if !enabled {
		return value, nil
	}
	if raw := strings.TrimSpace(labels["nsc.port"]); raw != "" {
		value.Port, err = strconv.Atoi(raw)
		if err != nil || value.Port < 1 || value.Port > 65535 {
			return value, fmt.Errorf("nsc.port must be in 1..65535")
		}
	}
	if raw := strings.ToLower(strings.TrimSpace(labels["nsc.protocol"])); raw != "" {
		if raw != "http" && raw != "https" {
			return value, fmt.Errorf("nsc.protocol must be http or https")
		}
		value.Protocol = raw
	}
	if raw := strings.ToLower(strings.TrimSpace(labels["nsc.readiness.mode"])); raw != "" {
		value.Readiness.Mode = model.ReadinessMode(raw)
	}
	if raw := strings.TrimSpace(labels["nsc.readiness.path"]); raw != "" {
		value.Readiness.Path = raw
	}
	if raw := strings.ToLower(strings.TrimSpace(labels["nsc.readiness.scheme"])); raw != "" {
		value.Readiness.Scheme = raw
	}
	for key, target := range map[string]*time.Duration{
		"nsc.readiness.interval": &value.Readiness.Interval, "nsc.readiness.timeout": &value.Readiness.Timeout,
		"nsc.readiness.startup-grace-period": &value.Readiness.StartupGracePeriod, "nsc.readiness.startup-timeout": &value.Readiness.StartupTimeout,
	} {
		if raw := strings.TrimSpace(labels[key]); raw != "" {
			parsed, parseErr := time.ParseDuration(raw)
			if parseErr != nil || parsed <= 0 {
				return value, fmt.Errorf("%s must be a positive duration", key)
			}
			*target = parsed
		}
	}
	if raw := strings.TrimSpace(labels["nsc.readiness.success-threshold"]); raw != "" {
		value.Readiness.SuccessThreshold, err = strconv.Atoi(raw)
		if err != nil || value.Readiness.SuccessThreshold < 1 {
			return value, fmt.Errorf("nsc.readiness.success-threshold must be positive")
		}
	}
	if raw := strings.TrimSpace(labels["nsc.readiness.failure-threshold"]); raw != "" {
		value.Readiness.FailureThreshold, err = strconv.Atoi(raw)
		if err != nil || value.Readiness.FailureThreshold < 1 {
			return value, fmt.Errorf("nsc.readiness.failure-threshold must be positive")
		}
	}
	if _, ok := labels["nsc.readiness.tcp-fallback"]; ok {
		value.Readiness.TCPFallback, err = parseBool(labels, "nsc.readiness.tcp-fallback", true)
		if err != nil {
			return value, err
		}
	}
	if err := validateLabelReadiness(value.Readiness); err != nil {
		return value, err
	}

	routeFields := map[string]map[string]string{}
	for key, raw := range labels {
		const prefix = "nsc.routes."
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		rest := strings.TrimPrefix(key, prefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return value, fmt.Errorf("invalid route label %q", key)
		}
		if routeFields[parts[0]] == nil {
			routeFields[parts[0]] = map[string]string{}
		}
		routeFields[parts[0]][parts[1]] = strings.TrimSpace(raw)
	}
	names := make([]string, 0, len(routeFields))
	for name := range routeFields {
		names = append(names, name)
	}
	sort.Strings(names)
	type hostEntryPolicy struct {
		tlsMode                   model.TLSMode
		httpEnabled, httpsEnabled bool
		redirect                  bool
	}
	policyByHost := make(map[string]hostEntryPolicy, len(names))
	for _, name := range names {
		fields := routeFields[name]
		host, err := normalizeLabelHost(fields["host"])
		if err != nil {
			return value, fmt.Errorf("route %q: %w", name, err)
		}
		routePath, err := normalizeLabelPath(fields["path"])
		if err != nil {
			return value, fmt.Errorf("route %q: %w", name, err)
		}
		tlsMode := model.TLSMode(strings.ToLower(fields["tls"]))
		if tlsMode == "" {
			tlsMode = model.TLSDisabled
		}
		if tlsMode != model.TLSDisabled && tlsMode != model.TLSManaged && tlsMode != model.TLSCustom {
			return value, fmt.Errorf("route %q: tls must be disabled, managed, or custom", name)
		}
		httpEnabled, httpsEnabled, err := parseLabelEntrypoints(fields["entrypoints"])
		if err != nil {
			return value, fmt.Errorf("route %q: %w", name, err)
		}
		if httpsEnabled && tlsMode == model.TLSDisabled {
			return value, fmt.Errorf("route %q: https entrypoint requires managed or custom tls", name)
		}
		if !httpsEnabled && tlsMode != model.TLSDisabled {
			return value, fmt.Errorf("route %q: managed or custom tls requires the https entrypoint", name)
		}
		redirect, err := parseFieldBool(fields, "redirect-http-to-https", false)
		if err != nil {
			return value, fmt.Errorf("route %q: %w", name, err)
		}
		if redirect && (!httpEnabled || !httpsEnabled) {
			return value, fmt.Errorf("route %q: redirect-http-to-https requires http and https entrypoints", name)
		}
		policy := hostEntryPolicy{tlsMode: tlsMode, httpEnabled: httpEnabled, httpsEnabled: httpsEnabled, redirect: redirect}
		if previous, ok := policyByHost[host]; ok && previous != policy {
			return value, fmt.Errorf("routes for host %q must use the same entrypoints, redirect, and tls mode", host)
		}
		policyByHost[host] = policy
		priority := 0
		if fields["priority"] != "" {
			priority, err = strconv.Atoi(fields["priority"])
			if err != nil {
				return value, fmt.Errorf("route %q: priority must be an integer", name)
			}
		}
		strip, err := parseFieldBool(fields, "strip-path", false)
		if err != nil {
			return value, fmt.Errorf("route %q: %w", name, err)
		}
		websocket, err := parseFieldBool(fields, "websocket", false)
		if err != nil {
			return value, fmt.Errorf("route %q: %w", name, err)
		}
		targetPort := 0
		if fields["port"] != "" {
			targetPort, err = strconv.Atoi(fields["port"])
			if err != nil || targetPort < 1 || targetPort > 65535 {
				return value, fmt.Errorf("route %q: port must be in 1..65535", name)
			}
		}
		protocol := strings.ToLower(fields["protocol"])
		if protocol == "" {
			protocol = value.Protocol
		}
		if protocol != "http" && protocol != "https" {
			return value, fmt.Errorf("route %q: protocol must be http or https", name)
		}
		serverName := ""
		if fields["server-name"] != "" {
			serverName, err = normalizeLabelHost(fields["server-name"])
			if err != nil {
				return value, fmt.Errorf("route %q: server-name must be a valid exact DNS name", name)
			}
		}
		value.Routes = append(value.Routes, model.DiscoveredRoute{ExternalKey: name, Host: host, Path: routePath, TLSMode: tlsMode,
			HTTPEnabled: httpEnabled, HTTPSEnabled: httpsEnabled, HTTPSRedirect: redirect,
			Priority: priority, StripPath: strip, TargetPort: targetPort, UpstreamProtocol: protocol, UpstreamServerName: serverName, WebsocketEnabled: websocket})
	}
	return value, nil
}

func parseLabelEntrypoints(raw string) (bool, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true, false, nil
	}
	var httpEnabled, httpsEnabled bool
	for _, item := range strings.Split(raw, ",") {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "http":
			if httpEnabled {
				return false, false, fmt.Errorf("entrypoints must not contain duplicates")
			}
			httpEnabled = true
		case "https":
			if httpsEnabled {
				return false, false, fmt.Errorf("entrypoints must not contain duplicates")
			}
			httpsEnabled = true
		default:
			return false, false, fmt.Errorf("entrypoints may only contain http and https")
		}
	}
	if !httpEnabled && !httpsEnabled {
		return false, false, fmt.Errorf("entrypoints must contain at least one value")
	}
	return httpEnabled, httpsEnabled, nil
}

func parseBool(labels map[string]string, key string, fallback bool) (bool, error) {
	raw, ok := labels[key]
	if !ok {
		return fallback, nil
	}
	value, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value, nil
}
func parseFieldBool(fields map[string]string, key string, fallback bool) (bool, error) {
	raw, ok := fields[key]
	if !ok || raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return value, nil
}

func normalizeLabelHost(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(value, ".")))
	if value == "" || strings.ContainsAny(value, "*/\\:#? \t\r\n") {
		return "", fmt.Errorf("host is required and must be an exact DNS name")
	}
	ascii, err := idna.Lookup.ToASCII(value)
	if err != nil {
		return "", fmt.Errorf("invalid IDNA host")
	}
	if len(ascii) > 253 || net.ParseIP(ascii) != nil {
		return "", fmt.Errorf("host must be a DNS name")
	}
	for _, label := range strings.Split(ascii, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", fmt.Errorf("host contains an invalid DNS label")
		}
	}
	return ascii, nil
}
func normalizeLabelPath(value string) (string, error) {
	const acmeChallengePath = "/.well-known/acme-challenge/"
	if value == "" {
		return "/", nil
	}
	if !strings.HasPrefix(value, "/") || !safeLabelPath(value) {
		return "", fmt.Errorf("path must be an absolute URL path")
	}
	value = path.Clean(value)
	if value != "/" {
		value = strings.TrimSuffix(value, "/") + "/"
	}
	if strings.HasPrefix(value, acmeChallengePath) {
		return "", fmt.Errorf("path is reserved for the ACME HTTP-01 system route")
	}
	return value, nil
}
func validateLabelReadiness(value model.ReadinessPolicy) error {
	switch value.Mode {
	case model.ReadinessAuto, model.ReadinessHTTP, model.ReadinessDocker, model.ReadinessTCP, model.ReadinessGrace:
	default:
		return fmt.Errorf("unsupported readiness mode %q", value.Mode)
	}
	if value.Mode == model.ReadinessHTTP && value.Path == "" {
		return fmt.Errorf("HTTP readiness requires nsc.readiness.path")
	}
	if value.Path != "" && (!strings.HasPrefix(value.Path, "/") || !safeLabelPath(value.Path)) {
		return fmt.Errorf("readiness path must be an absolute URL path without query or fragment")
	}
	if value.Scheme != "" && value.Scheme != "http" && value.Scheme != "https" {
		return fmt.Errorf("readiness scheme must be http or https")
	}
	if value.Timeout <= 0 || value.Interval <= 0 || value.Timeout > value.Interval {
		return fmt.Errorf("readiness timeout and interval are invalid")
	}
	return nil
}

func safeLabelPath(value string) bool {
	if strings.ContainsAny(value, "?#{};\"'\\") {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}
