package dto

// CreateIncidentEscalationRuleRequest 创建事件升级规则
type CreateIncidentEscalationRuleRequest struct {
	Name               string                 `json:"name" binding:"required"`
	Description        string                 `json:"description"`
	TriggerType        string                 `json:"triggerType" binding:"required"`
	EscalationLevel    int                    `json:"escalationLevel"`
	TriggerMinutes     int                    `json:"triggerMinutes"`
	TargetAssigneeType string                 `json:"targetAssigneeType"`
	AutoEscalate       bool                   `json:"autoEscalate"`
	NotificationConfig map[string]interface{} `json:"notificationConfig"`
	IsActive           bool                   `json:"isActive"`
	TenantID           int                    `json:"tenantId"`
	FromStatus         *string                `json:"fromStatus,omitempty"`
	ToStatus           *string                `json:"toStatus,omitempty"`
	TargetAssigneeID   *int                   `json:"targetAssigneeId,omitempty"`
	TargetGroup        *string                `json:"targetGroup,omitempty"`
	PriorityMatch      *string                `json:"priorityMatch,omitempty"`
	CategoryMatch      *string                `json:"categoryMatch,omitempty"`
}

// UpdateIncidentEscalationRuleRequest 更新事件升级规则
type UpdateIncidentEscalationRuleRequest struct {
	Name               *string                `json:"name,omitempty"`
	Description        *string                `json:"description,omitempty"`
	TriggerType        *string                `json:"triggerType,omitempty"`
	EscalationLevel    *int                   `json:"escalationLevel,omitempty"`
	TriggerMinutes     *int                   `json:"triggerMinutes,omitempty"`
	TargetAssigneeType *string                `json:"targetAssigneeType,omitempty"`
	AutoEscalate       *bool                  `json:"autoEscalate,omitempty"`
	NotificationConfig map[string]interface{} `json:"notificationConfig,omitempty"`
	IsActive           *bool                  `json:"isActive,omitempty"`
	FromStatus         *string                `json:"fromStatus,omitempty"`
	ToStatus           *string                `json:"toStatus,omitempty"`
	TargetAssigneeID   *int                   `json:"targetAssigneeId,omitempty"`
	TargetGroup        *string                `json:"targetGroup,omitempty"`
	PriorityMatch      *string                `json:"priorityMatch,omitempty"`
	CategoryMatch      *string                `json:"categoryMatch,omitempty"`
}

// UpdateKnownErrorRequest 更新已知错误
type UpdateKnownErrorRequest struct {
	Title            *string  `json:"title,omitempty"`
	Description      *string  `json:"description,omitempty"`
	Symptoms         *string  `json:"symptoms,omitempty"`
	RootCause        *string  `json:"rootCause,omitempty"`
	Workaround       *string  `json:"workaround,omitempty"`
	PermanentFix     *string  `json:"permanentFix,omitempty"`
	Resolution       *string  `json:"resolution,omitempty"`
	Category         *string  `json:"category,omitempty"`
	Status           *string  `json:"status,omitempty"`
	Severity         *string  `json:"severity,omitempty"`
	AffectedProducts *[]string `json:"affectedProducts,omitempty"`
	AffectedCIs *[]string    `json:"affectedCis,omitempty"`
	AffectedServices *string  `json:"affectedServices,omitempty"`
	Keywords        *[]string `json:"keywords,omitempty"`
}

// CreateEngineerSkillRequest 创建工程师技能
type CreateEngineerSkillRequest struct {
	UserID           int                    `json:"userId" binding:"required"`
	Category         string                 `json:"category" binding:"required"`
	SkillName        string                 `json:"skillName" binding:"required"`
	ProficiencyLevel int                 `json:"proficiencyLevel"`
	ExperienceYears  int                `json:"experienceYears"`
	Certifications   []string             `json:"certifications"`
	IsAvailable      bool                   `json:"isAvailable"`
	CurrentLoad      int                    `json:"currentLoad"`
	MaxLoad          int                    `json:"maxLoad"`
	WorkingHours     map[string]interface{} `json:"workingHours"`
	TenantID         int                    `json:"tenantId"`
	PreferredShift   *string                `json:"preferredShift,omitempty"`
}

// UpdateEngineerSkillRequest 更新工程师技能
type UpdateEngineerSkillRequest struct {
	Category         *string                `json:"category,omitempty"`
	SkillName        *string                `json:"skillName,omitempty"`
	ProficiencyLevel *int                `json:"proficiencyLevel,omitempty"`
	ExperienceYears  *int               `json:"experienceYears,omitempty"`
	Certifications   *[]string             `json:"certifications,omitempty"`
	IsAvailable      *bool                  `json:"isAvailable,omitempty"`
	CurrentLoad      *int                   `json:"currentLoad,omitempty"`
	MaxLoad          *int                   `json:"maxLoad,omitempty"`
	WorkingHours     *map[string]interface{} `json:"workingHours,omitempty"`
	PreferredShift   *string                `json:"preferredShift,omitempty"`
}
