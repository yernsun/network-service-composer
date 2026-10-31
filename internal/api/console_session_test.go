package api

import (
	"net/http"
	"testing"
)

func TestIndependentHTTPSListenerProtectsLoginAndLogoutCookies(t *testing.T) {
	for _, mode := range []string{"internal-http", "console-https", "http-tls", "public-https"} {
		t.Run(mode, func(t *testing.T) {
			server, handler := newTestServer(t)
			server.config.Server.PublicURL = "http://controller:8080"
			switch mode {
			case "console-https":
				server.config.Server.ConsoleHTTPSListen = ":8443"
			case "http-tls":
				server.config.Server.HTTPTLSCertFile = "fixture.crt"
			case "public-https":
				server.config.Server.PublicURL = "https://console.example.test"
			}
			cookie, csrf := browserLogin(t, handler)
			secure := mode != "internal-http"
			if cookie.Secure != secure {
				t.Fatal("login cookie transport disagrees with configured HTTPS")
			}
			response := browserRequest(handler, "POST", "/api/v1/auth/logout", "{}", cookie, csrf)
			if response.Code != http.StatusNoContent || len(response.Result().Cookies()) != 1 || response.Result().Cookies()[0].Secure != secure {
				t.Fatal("logout cookie transport differs")
			}
		})
	}
}
