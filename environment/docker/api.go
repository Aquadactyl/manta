package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"emperror.dev/errors"
	"github.com/containerd/errdefs/pkg/errhttp"
	"github.com/moby/moby/api/types/common"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/versions"

	"github.com/Aquadactyl/manta/config"
)

var (
	o           sync.Once
	cli         cliSettings
	fastEnabled bool
)

type cliSettings struct{ version string }

func configure(*client.Client) {
	o.Do(func() { fastEnabled = config.Get().Docker.UsePerformantInspect })
}

// ContainerInspect fetches container details through the Moby API client.
func (e *Environment) ContainerInspect(ctx context.Context) (container.InspectResponse, error) {
	configure(e.client)
	result, err := e.client.ContainerInspect(ctx, e.ID, client.ContainerInspectOptions{})
	return result.Container, err
}

func parseErrorFromResponse(res *http.Response, body []byte) error {
	if res.StatusCode >= 200 && res.StatusCode < 400 {
		return nil
	}
	if res.Header != nil && res.Header.Get("Content-Type") == "application/json" && (cli.version == "" || versions.GreaterThan(cli.version, "1.23")) {
		var response common.ErrorResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return errors.WithStack(err)
		}
		return errors.Wrap(errors.New(strings.TrimSpace(response.Message)), "Error response from daemon")
	}
	return errors.Wrap(errors.New(strings.TrimSpace(string(body))), "Error response from daemon")
}

type httpStatusError struct {
	err    error
	errdef error
}

func (e *httpStatusError) Error() string        { return e.err.Error() }
func (e *httpStatusError) Unwrap() error        { return e.err }
func (e *httpStatusError) Is(target error) bool { return errors.Is(e.errdef, target) }

func statusError(err error, statusCode int) error {
	if err == nil {
		return nil
	}
	return &httpStatusError{err: err, errdef: errhttp.ToNative(statusCode)}
}
