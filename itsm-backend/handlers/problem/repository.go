package problem

import (
	"context"
)

// Repository interface for Problem domain
type Repository interface {
	Create(ctx context.Context, p *Problem) (*Problem, error)
	Get(ctx context.Context, id int, tenantID int) (*Problem, error)
	GetWithAssociations(ctx context.Context, id int, tenantID int) (*Problem, error)
	List(ctx context.Context, tenantID int, page, size int, filters map[string]interface{}) ([]*Problem, int, error)
	Update(ctx context.Context, p *Problem) (*Problem, error)
	Delete(ctx context.Context, id int, tenantID int) error
	GetStats(ctx context.Context, tenantID int) (*ProblemStats, error)
	// AddAssociations 建立 Problem 与其它记录的关联。relatedType="ticket" 时写入
	// WorkItemRelation（relation_type="related_to"），需要知道操作人 actorUserID 用于
	// WorkItemRelation.created_by_id；relatedType="incident"/"change" 时沿用旧的 ent
	// 多对多 edge，不需要 actorUserID（保持这两个分支不变，见任务边界说明）。
	AddAssociations(ctx context.Context, tenantID, problemID, actorUserID int, relatedType string, relatedIDs []int) error
	RemoveAssociation(ctx context.Context, tenantID, problemID int, relatedType string, relatedID int) error
}
