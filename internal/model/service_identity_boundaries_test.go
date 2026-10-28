package model

import "testing"

func TestQualifiedDisplayIdentityNeverSuppliesGatewayDNS(t *testing.T) {
	for _, tc := range []struct{ name, namespace, qualified, want string }{
		{"web", "project", "", "project_web"}, {"project_web", "project", "", "project_web"}, {"web", "", "", "web"}, {"projector_web", "project", "", "project_projector_web"}, {"web", "project", "stable.identity", "stable.identity"}, {"", "", "", ""},
	} {
		t.Run(tc.want, func(t *testing.T) {
			s := Service{Name: tc.name, Namespace: tc.namespace, QualifiedName: tc.qualified}
			if got := s.EffectiveQualifiedName(); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if s.EffectiveDNSName() != "" {
				t.Fatal("display identity became an unverified upstream")
			}
		})
	}
}
