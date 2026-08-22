package service_catalog

import "testing"

func TestRequiresInfraFields(t *testing.T) {
	cases := []struct {
		serviceType string
		want        bool
	}{
		{"vm", true},
		{"network", true},
		{"database", true},
		{"rds", true},
		{"storage", true},
		{"oss", true},
		{"custom", false},
		{"access", false},
		{"security", false},
		{"software", false},
		{"devops", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.serviceType, func(t *testing.T) {
			got := RequiresInfraFields(tc.serviceType)
			if got != tc.want {
				t.Errorf("RequiresInfraFields(%q) = %v, want %v", tc.serviceType, got, tc.want)
			}
		})
	}
}
