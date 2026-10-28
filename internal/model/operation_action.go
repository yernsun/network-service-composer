package model

// OperationBusinessAction describes the accepted user action without changing
// the executor's operation type or persisting presentation state on its row.
func OperationBusinessAction(op DeploymentOperation, source DeploymentSource) string {
	if op.Type != "UPGRADE" {
		return op.Type
	}
	if source.RestoredFrom != "" {
		if op.ServiceID != "" {
			return "RESTORE_SERVICE"
		}
		return "RESTORE_APPLICATION"
	}
	if scope := source.ServiceScope; scope != nil {
		if scope.Mode == "delete" {
			return "DELETE_SERVICE"
		}
		if scope.RequestedReplicas != nil {
			return "SCALE"
		}
		switch scope.Mode {
		case "create":
			return "CREATE_SERVICES"
		case "update":
			return "UPDATE_SERVICES"
		}
	}
	return op.Type
}
