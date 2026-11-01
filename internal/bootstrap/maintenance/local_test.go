package maintenance

import (
	"crypto/rand"
	"errors"
	"github.com/yernsun/network-service-composer/internal/bootstrap"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLocalDockerTransportBoundsErrorsAndPullStreams(t *testing.T) {
	for _, scenario := range []string{"decode", "empty", "pull", "pull-error", "pull-corrupt", "status", "network", "decode-error", "drain-error", "encode", "request"} {
		t.Run(scenario, func(t *testing.T) {
			engine := NewLocalEngine()
			engine.Client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Host != "docker" || !strings.HasPrefix(request.URL.Path, "/v1.47/") {
					t.Fatal("transport left the fixed Docker API")
				}
				if scenario == "network" {
					return nil, errors.New("unavailable")
				}
				status := 200
				body := io.ReadCloser(io.NopCloser(strings.NewReader(`{"ID":"known"}`)))
				switch scenario {
				case "pull":
					body = io.NopCloser(strings.NewReader("{\"status\":\"pulled\"}\n"))
				case "pull-error":
					body = io.NopCloser(strings.NewReader(`{"error":"private registry details"}`))
				case "pull-corrupt", "decode-error":
					body = io.NopCloser(strings.NewReader("not-json"))
				case "status":
					status = 409
				case "drain-error":
					body = errorBody{}
				}
				return &http.Response{StatusCode: status, Body: body, Header: make(http.Header), Request: request}, nil
			})
			method, path := "GET", "/services/known"
			var input, output any
			var value struct{ ID string }
			if scenario == "decode" || scenario == "decode-error" {
				output = &value
			}
			if strings.HasPrefix(scenario, "pull") {
				method, path = "POST", "/images/create?fromImage=approved"
			}
			if scenario == "encode" {
				input = make(chan int)
			}
			if scenario == "request" {
				path = "/bad\npath"
			}
			err := engine.Engine(t.Context(), method, path, input, output)
			valid := scenario == "decode" || scenario == "empty" || scenario == "pull"
			if (err == nil) != valid {
				t.Fatal(scenario, err)
			}
			if scenario == "decode" && value.ID != "known" {
				t.Fatal("receipt was not decoded")
			}
			if err != nil && strings.Contains(err.Error(), "private registry details") {
				t.Fatal("pull diagnostics were exposed")
			}
		})
	}
}

type errorBody struct{}

func (errorBody) Read([]byte) (int, error) { return 0, errors.New("interrupted body") }
func (errorBody) Close() error             { return nil }

func TestLocalRegistryAuthorizationIsAHeaderNotDockerSpecOrDiagnostics(t *testing.T) {
	engine := NewLocalEngine()
	auth := rand.Text()
	engine.Client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Registry-Auth") != auth {
			t.Fatal("approved pull authorization is missing")
		}
		body, _ := io.ReadAll(request.Body)
		if strings.Contains(string(body), auth) || string(body) != `{"Name":"owned"}` {
			t.Fatal("registry credential entered the Docker specification")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})
	if err := engine.Engine(t.Context(), "POST", "/services/owned/update", bootstrap.RegistryRequest{Body: map[string]string{"Name": "owned"}, Authorization: auth}, nil); err != nil {
		t.Fatal(err)
	}
}
