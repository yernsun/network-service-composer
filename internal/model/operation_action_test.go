package model

import "testing"

func TestOperationBusinessActionPreservesExecutorTypeAndRestoreMeaning(t *testing.T) {
	count := 0
	for _, test := range []struct {
		name, kind, service, restored, mode, want string
		replicas                                  *int
	}{
		{name: "install", kind: "INSTALL", want: "INSTALL"},
		{name: "legacy service upgrade", kind: "UPGRADE", service: "web", want: "UPGRADE"},
		{name: "missing scope", kind: "UPGRADE", want: "UPGRADE"},
		{name: "unknown scope", kind: "UPGRADE", mode: "future", want: "UPGRADE"},
		{name: "create", kind: "UPGRADE", mode: "create", want: "CREATE_SERVICES"},
		{name: "update", kind: "UPGRADE", mode: "update", want: "UPDATE_SERVICES"},
		{name: "delete", kind: "UPGRADE", mode: "delete", want: "DELETE_SERVICE"},
		{name: "persist zero", kind: "UPGRADE", mode: "update", replicas: &count, want: "SCALE"},
		{name: "service restore", kind: "UPGRADE", service: "web", restored: "historical", mode: "update", replicas: &count, want: "RESTORE_SERVICE"},
		{name: "application restore", kind: "UPGRADE", restored: "historical", mode: "update", want: "RESTORE_APPLICATION"},
		{name: "runtime removal", kind: "REMOVE_RUNTIME", service: "web", want: "REMOVE_RUNTIME"},
		{name: "future executor", kind: "FUTURE", restored: "historical", want: "FUTURE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := DeploymentOperation{Type: test.kind, ServiceID: test.service}
			source := DeploymentSource{RestoredFrom: test.restored}
			if test.mode != "" {
				source.ServiceScope = &ServiceDeploymentScope{Mode: test.mode, RequestedReplicas: test.replicas}
			}
			if action := OperationBusinessAction(op, source); action != test.want || op.Type != test.kind {
				t.Fatal(action, op)
			}
		})
	}
}
