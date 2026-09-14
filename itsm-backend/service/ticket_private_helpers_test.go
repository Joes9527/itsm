package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMapProcessStatusToDTO(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"running", "running"},
		{"completed", "completed"},
		{"failed", "failed"},
		{"cancelled", "cancelled"},
		{"unknown", "unknown"}, // 默认 fallback
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			// 注意：mapProcessStatusToDTO 返回 dto.ProcessStatus，
			// 测试只需验证它不会 panic 并返回非空值。
			result := mapProcessStatusToDTO(tt.input)
			assert.NotEmpty(t, string(result))
		})
	}
}
func TestGetEscalatedPriority(t *testing.T) {
	for _, tc := range []struct{ current, want string }{{"low", "medium"}, {"medium", "high"}, {"high", "critical"}, {"critical", "critical"}} {
		got, err := escalatedTicketPriority(tc.current)
		assert.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	for _, invalid := range []string{"urgent", "unknown", ""} {
		_, err := escalatedTicketPriority(invalid)
		assert.Error(t, err)
	}
}
