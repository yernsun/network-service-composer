// Package notification delivers signed, bounded outbound webhooks.
package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/yernsun/network-service-composer/internal/networkpolicy"
)

// ValidateEndpoint never returns submitted URLs, which may contain credentials.
func ValidateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(endpoint) > 4096 {
		return errors.New("a public HTTPS webhook URL without userinfo or fragment is required")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !publicAddress(ip) {
		return errors.New("webhook address must be public")
	}
	return nil
}
func publicAddress(ip netip.Addr) bool {
	return networkpolicy.PublicAddress(ip)
}

// Resolve and pin every actual connection; neither redirects nor proxies may
// bypass the address check. Private receivers are outside this initial contract.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, errors.New("webhook DNS lookup failed")
	}
	for _, ip := range addresses {
		if !publicAddress(ip) {
			return nil, errors.New("webhook DNS returned a non-public address")
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("webhook DNS returned no addresses")
	}
	return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
}

type Result struct {
	Delivered  bool
	Retryable  bool
	Code       string
	RetryAfter time.Duration
}
type Sender interface {
	Send(context.Context, string, string, string, []byte, time.Time) Result
}
type Webhook struct{ Client *http.Client }

func (w Webhook) Send(ctx context.Context, endpoint, secret, eventID string, body []byte, now time.Time) Result {
	if ValidateEndpoint(endpoint) != nil {
		return Result{Code: "invalid_endpoint"}
	}
	client := w.Client
	if client == nil {
		transport := &http.Transport{DialContext: publicDial, TLSHandshakeTimeout: 5 * time.Second, DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	}
	// Copy an injected client so even test/custom transports cannot follow redirects.
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{Code: "invalid_endpoint"}
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%s.%s.", timestamp, eventID)
	_, _ = mac.Write(body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-NSC-Event-ID", eventID)
	req.Header.Set("X-NSC-Timestamp", timestamp)
	req.Header.Set("X-NSC-Signature", hex.EncodeToString(mac.Sum(nil)))
	response, err := bounded.Do(req)
	if err != nil {
		return Result{Retryable: true, Code: "transport_error"}
	}
	defer response.Body.Close()
	// Receiver bodies may contain credentials or HTML; never retain them.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	status := response.StatusCode
	if status >= 200 && status < 300 {
		return Result{Delivered: true}
	}
	result := Result{Code: "http_" + strconv.Itoa(status), Retryable: status == 408 || status == 429 || status >= 500}
	if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 {
		result.RetryAfter = time.Duration(min(seconds, 86400)) * time.Second
	} else if at, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
		result.RetryAfter = max(0, min(24*time.Hour, at.Sub(now)))
	}
	return result
}
