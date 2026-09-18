package dto

import "time"

// SystemConfigRequest 请求
type SystemConfigRequest struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	ValueType   string `json:"valueType"`
	Category    string `json:"category"`
	Description string `json:"description"`
	TenantID    int    `json:"tenantId"`
}

// SystemConfigResponse 响应
type SystemConfigResponse struct {
	ID          int       `json:"id"`
	Key         string    `json:"key"`
	Value       string    `json:"value"`
	ValueType   string    `json:"valueType"`
	Category    string    `json:"category"`
	Description string    `json:"description"`
	CreatedBy   string    `json:"createdBy"`
	TenantID    int       `json:"tenantId"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// SystemConfigListResponse 列表响应
type SystemConfigListResponse struct {
	Configs []SystemConfigResponse `json:"configs"`
	Total   int                    `json:"total"`
	Page    int                    `json:"page"`
	Size    int                    `json:"size"`
}

// UpdateSystemConfigRequest 更新请求
type UpdateSystemConfigRequest struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	ValueType   string `json:"valueType"`
	Description string `json:"description"`
}

// SetCTIGovernanceRequest 受控启用 CTI 门禁。
// 刻意不接受 effectiveFrom：截止时间只能由后端在第一次启用时派生，之后不可修改。
type SetCTIGovernanceRequest struct {
	CatalogEnforced    bool `json:"catalogEnforced"`
	CompletionEnforced bool `json:"completionEnforced"`
}

// CTIGovernanceResponse 是启用记录与恢复盘点的响应。
type CTIGovernanceResponse struct {
	CatalogEnforced    bool   `json:"catalogEnforced"`
	CompletionEnforced bool   `json:"completionEnforced"`
	EffectiveFrom      string `json:"effectiveFrom,omitempty"`
	Applied            bool   `json:"applied"`
	// InFlightWithoutClassification 是恢复启用时在途且完全没有分类的工单数下界。
	InFlightWithoutClassification int `json:"inFlightWithoutClassification"`
}
