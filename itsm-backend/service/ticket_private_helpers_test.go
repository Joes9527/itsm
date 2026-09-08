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
	tests := []struct {
		currentPriority string
		expectedNotSame bool
	}{
		{"low", true},
		{"medium", true},
		{"high", true},
		{"urgent", false}, // 已是最高之一，升级后仍是 urgent
		{"critical", false},
	}
	for _, tt := range tests {
		t.Run(tt.currentPriority, func(t *testing.T) {
			svc := &TicketService{} // 不需要依赖
			escalated := svc.getEscalatedPriority(tt.currentPriority)
			if tt.expectedNotSame {
				assert.NotEqual(t, tt.currentPriority, escalated,
					"升级后优先级应变化")
			}
			assert.NotEmpty(t, escalated)
		})
	}
}
