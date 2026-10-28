package deployment

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/yernsun/network-service-composer/internal/model"
	"github.com/yernsun/network-service-composer/internal/storage"
)

func (w *Worker) checkServiceVersions(ctx context.Context, runtime model.Runtime, value material) error {
	scope := value.revision.ConfigurationSource.ServiceScope
	if scope == nil || scope.Observed == nil {
		return errors.New("service review is missing Docker version evidence")
	}
	args := w.dockerArgs(runtime)
	names, err := w.runner.Run(ctx, ".", nil, append(args, "service", "ls", "--format", "{{.Name}}"))
	if err != nil {
		return errors.New("cannot verify Docker service names")
	}
	existing := map[string]bool{}
	for _, name := range strings.Split(names, "\n") {
		existing[strings.TrimSpace(name)] = true
	}
	for _, name := range scope.Names {
		expected, recorded := scope.Observed[name]
		if !recorded {
			return storage.ErrPrecondition
		}
		qualified := value.application.ProjectName + "_" + name
		if !existing[qualified] {
			// A retried deletion may resume after Docker removed the service but
			// before the revision was activated. Absence is the desired runtime
			// state; a recreated object is still rejected by the checks below.
			if expected.ID != "" && scope.Mode != "delete" {
				return storage.ErrPrecondition
			}
			continue
		}
		if scope.Mode == "create" || expected.ID == "" {
			return storage.ErrConflict
		}
		raw, err := w.runner.Run(ctx, ".", nil, append(args, "service", "inspect", "--format", "{{.ID}}|{{.Version.Index}}|{{index .Spec.Labels \"com.docker.stack.namespace\"}}", qualified))
		if err != nil {
			return errors.New("cannot verify Docker service version")
		}
		parts := strings.Split(strings.TrimSpace(raw), "|")
		if len(parts) != 3 {
			return errors.New("invalid Docker service version evidence")
		}
		version, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || parts[0] != expected.ID || version != expected.Version || parts[2] != value.application.ProjectName || parts[2] != expected.Stack {
			return storage.ErrPrecondition
		}
	}
	return nil
}
