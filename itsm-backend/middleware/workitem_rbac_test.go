package middleware

import "testing"

func TestResourceForRecordClass(t *testing.T) {
	cases := []struct {
		recordClass string
		want        string
	}{
		{"incident", "incident"},
		{"problem", "problem"},
		{"change_request", "change"},
		{"generic", "ticket"},
		{"service_request_item", "ticket"},
		{"catalog_task", "ticket"},
		{"", "ticket"},
		{"some_future_value", "ticket"},
	}
	for _, tc := range cases {
		t.Run(tc.recordClass, func(t *testing.T) {
			got := resourceForRecordClass(tc.recordClass)
			if got != tc.want {
				t.Errorf("resourceForRecordClass(%q) = %q, want %q", tc.recordClass, got, tc.want)
			}
		})
	}
}
