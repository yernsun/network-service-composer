package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/yernsun/network-service-composer/internal/bootstrap"
)

type LocalEngine struct{ Client *http.Client }

func NewLocalEngine() *LocalEngine {
	return &LocalEngine{Client: &http.Client{Transport: &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}, Timeout: 2 * time.Minute}}
}
func (e *LocalEngine) Engine(ctx context.Context, method, path string, input, output any) error {
	authorization := ""
	if private, ok := input.(bootstrap.RegistryRequest); ok {
		input, authorization = private.Body, private.Authorization
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.47"+path, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if authorization != "" {
		request.Header.Set("X-Registry-Auth", authorization)
	}
	response, err := e.Client.Do(request)
	if err != nil {
		return fmt.Errorf("local Docker request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Docker returned HTTP %d", response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output)
	}
	// Image pull uses a JSON stream; an HTTP 200 can still contain a pull error.
	if path != "" && len(path) >= 14 && path[:14] == "/images/create" {
		decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
		for {
			var event struct {
				Error string `json:"error"`
			}
			err := decoder.Decode(&event)
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if event.Error != "" {
				return fmt.Errorf("approved image pull failed")
			}
		}
	}
	_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 16<<20))
	return err
}
