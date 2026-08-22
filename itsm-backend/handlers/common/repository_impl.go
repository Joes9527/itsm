package common

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
	"itsm-backend/ent/department"
	"itsm-backend/ent/tag"
	"itsm-backend/ent/team"
	"itsm-backend/ent/user"
)

type EntRepository struct {
	client *ent.Client
}

func NewEntRepository(client *ent.Client) *EntRepository {
	return &EntRepository{client: client}
}

// Mappings

func toUserDomain(e *ent.User) *User {
	if e == nil {
		return nil
	}
	mspRole := ""
	if e.MspRole != "" {
		mspRole = string(e.MspRole)
	}
	return &User{
		ID:           e.ID,
		Username:     e.Username,
		Email:        e.Email,
		Name:         e.Name,
		Role:         string(e.Role),
		MSPRole:      &mspRole,
		Department:   e.Department,
		DepartmentID: e.DepartmentID,
		Phone:        e.Phone,
		Active:       e.Active,
		TenantID:     e.TenantID,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
}

func toDeptDomain(e *ent.Department) *Department {
	if e == nil {
		return nil
	}
	d := &Department{
		ID:          e.ID,
		Name:        e.Name,
		Code:        e.Code,
		Description: e.Description,
		AreaName:    e.AreaName,
		OrgType:     e.OrgType,
		ManagerID:   e.ManagerID,
		ParentID:    e.ParentID,
		TenantID:    e.TenantID,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
	for _, child := range e.Edges.Children {
		d.Children = append(d.Children, toDeptDomain(child))
	}
	return d
}

func toTeamDomain(e *ent.Team) *Team {
	if e == nil {
		return nil
	}
	return &Team{
		ID:          e.ID,
		Name:        e.Name,
		Code:        e.Code,
		Description: e.Description,
		Status:      e.Status,
		ManagerID:   e.ManagerID,
		TenantID:    e.TenantID,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func toTagDomain(e *ent.Tag) *Tag {
	if e == nil {
		return nil
	}
	return &Tag{
		ID:          e.ID,
		Name:        e.Name,
		Code:        e.Code,
		Description: e.Description,
		Color:       e.Color,
		TenantID:    e.TenantID,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func toAuditLogDomain(e *ent.AuditLog) *AuditLog {
	if e == nil {
		return nil
	}
	body := ""
	if e.RequestBody != nil {
		body = *e.RequestBody
	}
	return &AuditLog{
		ID:          e.ID,
		CreatedAt:   e.CreatedAt,
		TenantID:    e.TenantID,
		UserID:      e.UserID,
		RequestID:   e.RequestID,
		IP:          e.IP,
		Resource:    e.Resource,
		Action:      e.Action,
		Path:        e.Path,
		Method:      e.Method,
		StatusCode:  e.StatusCode,
		RequestBody: body,
	}
}

// User methods

func (r *EntRepository) GetUserByUsername(ctx context.Context, username string, tenantID int) (*User, error) {
	e, err := r.client.User.Query().
		Where(user.Username(username), user.TenantID(tenantID)).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return toUserDomain(e), nil
}

func (r *EntRepository) GetUserByID(ctx context.Context, id int) (*User, error) {
	e, err := r.client.User.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return toUserDomain(e), nil
}

func (r *EntRepository) ListUsers(ctx context.Context, tenantID int) ([]*User, error) {
	es, err := r.client.User.Query().Where(user.TenantID(tenantID)).All(ctx)
	if err != nil {
		return nil, err
	}
	var res []*User
	for _, e := range es {
		res = append(res, toUserDomain(e))
	}
	return res, nil
}

func (r *EntRepository) CreateUser(ctx context.Context, u *User) (*User, error) {
	e, err := r.client.User.Create().
		SetUsername(u.Username).
		SetEmail(u.Email).
		SetName(u.Name).
		SetRole(u.Role).
		SetDepartment(u.Department).
		SetDepartmentID(u.DepartmentID).
		SetPhone(u.Phone).
		SetPasswordHash(""). // Service will set this
		SetTenantID(u.TenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toUserDomain(e), nil
}

func (r *EntRepository) UpdateUser(ctx context.Context, u *User) (*User, error) {
	e, err := r.client.User.UpdateOneID(u.ID).
		Where(user.TenantID(u.TenantID)).
		SetEmail(u.Email).
		SetName(u.Name).
		SetRole(u.Role).
		SetDepartment(u.Department).
		SetDepartmentID(u.DepartmentID).
		SetPhone(u.Phone).
		SetActive(u.Active).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toUserDomain(e), nil
}

// Department methods

func (r *EntRepository) CreateDepartment(ctx context.Context, d *Department) (*Department, error) {
	exists, err := r.client.Department.Query().
		Where(department.Code(d.Code), department.TenantID(d.TenantID), department.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("department code already exists: %s", d.Code)
	}
	builder := r.client.Department.Create().
		SetName(d.Name).
		SetCode(d.Code).
		SetDescription(d.Description).
		SetManagerID(d.ManagerID).
		SetTenantID(d.TenantID)

	if d.ParentID != 0 {
		builder.SetParentID(d.ParentID)
	}

	e, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	return toDeptDomain(e), nil
}

func (r *EntRepository) GetDepartment(ctx context.Context, id int, tenantID int) (*Department, error) {
	e, err := r.client.Department.Query().
		Where(department.ID(id), department.TenantID(tenantID), department.DeletedAtIsNil()).
		WithChildren(func(q *ent.DepartmentQuery) { q.Where(department.DeletedAtIsNil()) }).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return toDeptDomain(e), nil
}

func (r *EntRepository) ListDepartments(ctx context.Context, tenantID int) ([]*Department, error) {
	es, err := r.client.Department.Query().Where(department.TenantID(tenantID), department.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	var res []*Department
	for _, e := range es {
		res = append(res, toDeptDomain(e))
	}
	return res, nil
}

// GetDepartmentTree 返回该租户下完整的部门树。真实导入的组织数据层级可以深达十几层
// （远超 legacy 演示部门 2-3 层的规模），之前用 WithChildren 只 eager-load 一层子部门，
// 深层节点会被静默截断（Ent 没加载的 Edges.Children 是零值切片，toDeptDomain 递归到
// 那里就停了，不会报错，只是数据不全）。改成一次性拉全租户部门（几千行量级，单表单
// 索引查询，比按层链式 WithChildren 更便宜），在内存里按 parent_id 分组建树，
// 层数不再受查询写法限制。
func (r *EntRepository) GetDepartmentTree(ctx context.Context, tenantID int) ([]*Department, error) {
	es, err := r.client.Department.Query().
		Where(department.TenantID(tenantID), department.DeletedAtIsNil()).
		All(ctx)
	if err != nil {
		return nil, err
	}

	byID := make(map[int]*Department, len(es))
	childrenByParent := make(map[int][]*Department, len(es))
	var roots []*Department
	for _, e := range es {
		d := toDeptDomain(e)
		d.Children = nil
		byID[d.ID] = d
		if e.ParentID == 0 {
			roots = append(roots, d)
		} else {
			childrenByParent[e.ParentID] = append(childrenByParent[e.ParentID], d)
		}
	}
	// 孤儿节点（parent_id 指向的部门不存在或已被软删）当根节点处理，而不是静默丢弃——
	// 导入数据里父节点缺失是可能出现的脏数据，丢掉整个子树比多显示一个根更容易漏查。
	for parentID, kids := range childrenByParent {
		if _, ok := byID[parentID]; !ok {
			roots = append(roots, kids...)
		}
	}
	for _, d := range byID {
		if kids, ok := childrenByParent[d.ID]; ok {
			d.Children = kids
		}
	}
	return roots, nil
}

func (r *EntRepository) UpdateDepartment(ctx context.Context, d *Department) (*Department, error) {
	exists, err := r.client.Department.Query().
		Where(department.Code(d.Code), department.TenantID(d.TenantID), department.DeletedAtIsNil(), department.IDNEQ(d.ID)).
		Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("department code already exists: %s", d.Code)
	}
	builder := r.client.Department.UpdateOneID(d.ID).
		Where(department.TenantID(d.TenantID), department.DeletedAtIsNil()).
		SetName(d.Name).
		SetCode(d.Code).
		SetDescription(d.Description).
		SetManagerID(d.ManagerID)

	if d.ParentID != 0 {
		builder.SetParentID(d.ParentID)
	} else {
		builder.ClearParent()
	}

	e, err := builder.Save(ctx)
	if err != nil {
		return nil, err
	}
	return toDeptDomain(e), nil
}

func (r *EntRepository) DeleteDepartment(ctx context.Context, id int, tenantID int) error {
	_, err := r.client.Department.Update().
		Where(department.ID(id), department.TenantID(tenantID), department.DeletedAtIsNil()).
		SetDeletedAt(time.Now()).
		Save(ctx)
	return err
}

// Team methods

func (r *EntRepository) CreateTeam(ctx context.Context, t *Team) (*Team, error) {
	exists, err := r.client.Team.Query().
		Where(team.Code(t.Code), team.TenantID(t.TenantID), team.DeletedAtIsNil()).
		Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("team code already exists: %s", t.Code)
	}
	e, err := r.client.Team.Create().
		SetName(t.Name).
		SetCode(t.Code).
		SetDescription(t.Description).
		SetManagerID(t.ManagerID).
		SetTenantID(t.TenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toTeamDomain(e), nil
}

func (r *EntRepository) GetTeam(ctx context.Context, id int, tenantID int) (*Team, error) {
	e, err := r.client.Team.Query().Where(team.ID(id), team.TenantID(tenantID), team.DeletedAtIsNil()).Only(ctx)
	if err != nil {
		return nil, err
	}
	return toTeamDomain(e), nil
}

func (r *EntRepository) ListTeams(ctx context.Context, tenantID int) ([]*Team, error) {
	es, err := r.client.Team.Query().Where(team.TenantID(tenantID), team.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	var res []*Team
	for _, e := range es {
		res = append(res, toTeamDomain(e))
	}
	return res, nil
}

func (r *EntRepository) UpdateTeam(ctx context.Context, t *Team) (*Team, error) {
	exists, err := r.client.Team.Query().
		Where(team.Code(t.Code), team.TenantID(t.TenantID), team.DeletedAtIsNil(), team.IDNEQ(t.ID)).
		Exist(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("team code already exists: %s", t.Code)
	}
	e, err := r.client.Team.UpdateOneID(t.ID).
		Where(team.TenantID(t.TenantID), team.DeletedAtIsNil()).
		SetName(t.Name).
		SetCode(t.Code).
		SetDescription(t.Description).
		SetStatus(t.Status).
		SetManagerID(t.ManagerID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toTeamDomain(e), nil
}

func (r *EntRepository) DeleteTeam(ctx context.Context, id int, tenantID int) error {
	_, err := r.client.Team.Update().
		Where(team.ID(id), team.TenantID(tenantID), team.DeletedAtIsNil()).
		SetDeletedAt(time.Now()).
		Save(ctx)
	return err
}

func (r *EntRepository) AddTeamMember(ctx context.Context, teamID int, userID int) error {
	return r.client.Team.UpdateOneID(teamID).Where(team.DeletedAtIsNil()).AddUserIDs(userID).Exec(ctx)
}

// Tag methods

func (r *EntRepository) CreateTag(ctx context.Context, t *Tag) (*Tag, error) {
	e, err := r.client.Tag.Create().
		SetName(t.Name).
		SetCode(t.Code).
		SetDescription(t.Description).
		SetColor(t.Color).
		SetTenantID(t.TenantID).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toTagDomain(e), nil
}

func (r *EntRepository) ListTags(ctx context.Context, tenantID int) ([]*Tag, error) {
	es, err := r.client.Tag.Query().Where(tag.TenantID(tenantID)).All(ctx)
	if err != nil {
		return nil, err
	}
	var res []*Tag
	for _, e := range es {
		res = append(res, toTagDomain(e))
	}
	return res, nil
}

func (r *EntRepository) DeleteTag(ctx context.Context, id int, tenantID int) error {
	_, err := r.client.Tag.Delete().Where(tag.ID(id), tag.TenantID(tenantID)).Exec(ctx)
	return err
}

// Audit Log methods

func (r *EntRepository) CreateAuditLog(ctx context.Context, l *AuditLog) error {
	return r.client.AuditLog.Create().
		SetTenantID(l.TenantID).
		SetUserID(l.UserID).
		SetRequestID(l.RequestID).
		SetIP(l.IP).
		SetResource(l.Resource).
		SetAction(l.Action).
		SetPath(l.Path).
		SetMethod(l.Method).
		SetStatusCode(l.StatusCode).
		SetNillableRequestBody(&l.RequestBody).
		Exec(ctx)
}

func (r *EntRepository) ListAuditLogs(ctx context.Context, tenantID int, userID int, limit int) ([]*AuditLog, error) {
	q := r.client.AuditLog.Query().Where(auditlog.TenantID(tenantID))
	if userID != 0 {
		q = q.Where(auditlog.UserID(userID))
	}
	es, err := q.Order(ent.Desc(auditlog.FieldCreatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, err
	}
	// 返回空切片而非 nil，避免 JSON 序列化为 null 导致前端崩溃
	res := make([]*AuditLog, 0, len(es))
	for _, e := range es {
		res = append(res, toAuditLogDomain(e))
	}
	return res, nil
}
