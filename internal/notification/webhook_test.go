package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestWebhookURLAndAddressBoundaries(t *testing.T) {
	for _, endpoint := range []string{"", "://bad", "https://", "http://example.test/hook", "https://user:secret@example.test", "https://example.test/#secret", "https://127.0.0.1/hook", "https://[::1]", "https://169.254.169.254/token", "https://10.0.0.1/", "https://[::ffff:127.0.0.1]", "https://example.test/" + strings.Repeat("x", 4096)} {
		if err := ValidateEndpoint(endpoint); err == nil || strings.Contains(err.Error(), endpoint) && endpoint != "" {
			t.Fatalf("unsafe endpoint accepted or leaked: %q %v", endpoint, err)
		}
	}
	if err := ValidateEndpoint("https://8.8.8.8/hook"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEndpoint("https://receiver.example.test/hook?token=private"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"0.0.0.0", "127.0.0.1", "10.1.1.1", "::1", "fe80::1", "fc00::1", "224.0.0.1"} {
		if publicAddress(netip.MustParseAddr(address)) {
			t.Fatal("non-public address accepted")
		}
	}
	if !publicAddress(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
	for _, address := range []string{"bad address", "localhost:443", "100.64.0.1:443", "[::ffff:100.64.0.1]:443", "198.18.0.1:443", "[2001:db8::1]:443"} {
		if conn, err := publicDial(context.Background(), "tcp", address); err == nil {
			conn.Close()
			t.Fatal("private or invalid connection permitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := publicDial(ctx, "tcp", "receiver.invalid:443"); err == nil {
		t.Fatal("cancelled DNS lookup accepted")
	}
	if _, err := publicDial(ctx, "tcp", "8.8.8.8:443"); err == nil {
		t.Fatal("cancelled public connection accepted")
	}
}

func TestWebhookRejectsSpecialPurposeLiterals(t *testing.T) {
	for _, address := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "240.0.0.1", "[::ffff:100.64.0.1]", "[64:ff9b::a00:1]", "[2001:db8::1]"} {
		if ValidateEndpoint("https://"+address+"/hook") == nil {
			t.Fatalf("non-public receiver accepted: %s", address)
		}
	}
}
func TestSignedWebhookRealReceiverAndRedirectRefusal(t *testing.T) {
	const secret = "receiver-signing-secret"
	now := time.Unix(1800000000, 0)
	body := []byte(`{"id":"event-1","type":"image.available"}`)
	calls := 0
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != string(body) || r.Method != "POST" || r.Header.Get("X-NSC-Event-ID") != "event-1" {
			t.Error("event identity/body changed")
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte("1800000000.event-1."))
		_, _ = mac.Write(raw)
		if !hmac.Equal([]byte(r.Header.Get("X-NSC-Signature")), []byte(hex.EncodeToString(mac.Sum(nil)))) {
			t.Error("invalid signature")
		}
		if calls == 1 {
			w.Header().Set("Location", "https://another.example.test/secret")
			w.WriteHeader(302)
			return
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	origin, _ := url.Parse(receiver.URL)
	client := receiver.Client()
	transport := client.Transport
	client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Host = origin.Host
		clone.URL = &u
		return transport.RoundTrip(clone)
	})
	sender := Webhook{Client: client}
	result := sender.Send(context.Background(), "https://example.test/webhook", secret, "event-1", body, now)
	if result.Delivered || result.Retryable || result.Code != "http_302" || calls != 1 {
		t.Fatal("redirect was followed or accepted", result, calls)
	}
	if result = sender.Send(context.Background(), "https://example.test/webhook", secret, "event-1", body, now); !result.Delivered || calls != 2 {
		t.Fatal(result, calls)
	}
}
func TestWebhookFailureClassificationAndBoundedRetryAfter(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		status    int
		retry     string
		retryable bool
		delay     time.Duration
	}{{101, "", false, 0}, {400, "", false, 0}, {408, "", true, 0}, {429, "120", true, 2 * time.Minute}, {503, "99999999", true, 24 * time.Hour}, {500, now.Add(time.Hour).Format(http.TimeFormat), true, time.Hour}, {502, "bad", true, 0}, {401, "-1", false, 0}} {
		client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{tc.retry}}, Body: io.NopCloser(strings.NewReader("private receiver diagnostics"))}, nil
		})}
		result := (Webhook{Client: client}).Send(context.Background(), "https://receiver.example.test/hook?secret=private", "private-signing-secret", "event", []byte(`{}`), now)
		if result.Retryable != tc.retryable || result.Delivered || result.RetryAfter < tc.delay-time.Second || result.RetryAfter > tc.delay+time.Second || strings.Contains(result.Code, "private") {
			t.Fatal(tc, result)
		}
	}
	failed := Webhook{Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private target and credentials") })}}
	if result := failed.Send(context.Background(), "https://example.test/hook?secret=private", "secret", "event", nil, now); !result.Retryable || result.Code != "transport_error" {
		t.Fatal(result)
	}
	if result := failed.Send(context.Background(), "http://127.0.0.1", "secret", "event", nil, now); result.Retryable || result.Code != "invalid_endpoint" {
		t.Fatal(result)
	}
	// Default transport honors cancellation without reaching a live receiver.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := (Webhook{}).Send(ctx, "https://127.0.0.2.nip.invalid", "secret", "event", nil, now); result.Code != "transport_error" {
		t.Fatal(result)
	}
}
