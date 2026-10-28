package api

import (
	"context"
	"maps"

	"github.com/yernsun/network-service-composer/internal/model"
)

func (s *Server) operationResponse(ctx context.Context, op model.DeploymentOperation) model.DeploymentOperation {
	op.BusinessAction = op.Type
	if op.Type == "UPGRADE" && op.RevisionID != "" {
		revision, err := s.store.GetApplicationRevision(ctx, op.RevisionID)
		if err == nil && revision.ApplicationID == op.ApplicationID {
			op.BusinessAction = model.OperationBusinessAction(op, revision.ConfigurationSource)
		}
	}
	return op
}

func (s *Server) operationResponseValue(ctx context.Context, value any) any {
	switch v := value.(type) {
	case model.DeploymentOperation:
		return s.operationResponse(ctx, v)
	case *model.DeploymentOperation:
		if v != nil {
			return s.operationResponse(ctx, *v)
		}
	case map[string]any:
		values := maps.Clone(v)
		for key, item := range values {
			values[key] = s.operationResponseValue(ctx, item)
		}
		return values
	}
	return value
}

func (s *Server) operationResponses(ctx context.Context, operations []model.DeploymentOperation) []model.DeploymentOperation {
	values := make([]model.DeploymentOperation, 0, len(operations))
	for _, operation := range operations {
		values = append(values, s.operationResponse(ctx, operation))
	}
	return values
}
