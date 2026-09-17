package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"itsm-backend/ent"
	"itsm-backend/ent/ticketcategory"
)

// TicketCategoryService 工单分类（CTI）服务。
//
// 本服务是分类树不变量的唯一所有者：路径解析、三级上限、父子/租户一致性、引用保护与
// 维护事务。纯结构语义在 ticket_category_policy.go，引用扫描在
// ticket_category_references.go。分派/SLA/BPMN 规则仍由各自所有者维护。
type TicketCategoryService struct {
	client *ent.Client
}

// NewTicketCategoryService 创建工单分类服务实例
func NewTicketCategoryService(client *ent.Client) *TicketCategoryService {
	return &TicketCategoryService{client: client}
}

// ctiRowLock 在 PostgreSQL 上锁住被读取的分类行，使维护动作与工单/目录的并发创建
// 在同一行锁上串行化。SQLite 没有锁子句，仅用于单元 fixture；真实并发语义由 PostgreSQL
// 集成测试覆盖。
func ctiRowLock(selector *entsql.Selector) {
	if selector.Dialect() == dialect.Postgres {
		selector.ForUpdate()
	}
}

func (s *TicketCategoryService) withTx(ctx context.Context, fn func(tx *ent.Tx) error) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ResolveCTIPath 在调用方事务内解析 selectedID 的完整根路径（根→所选节点）。
//
// selectedID <= 0 表示未分类：requireFull 为真时失败，否则返回 nil。
// requireFull 为真要求完整三级；requireActive 为真要求所有节点当前启用，并在同一事务内
// 锁住路径行，防止并发停用/删除产生无效引用。requireActive 为假用于历史有效路径的质量
// 校验：停用不追溯撤销既有的合法完整路径。
func (s *TicketCategoryService) ResolveCTIPath(ctx context.Context, tx *ent.Tx, tenantID, selectedID int, requireFull, requireActive bool) ([]CTINode, error) {
	if tx == nil {
		return nil, errors.New("CTI resolution requires the owning transaction")
	}
	if tenantID <= 0 {
		return nil, ErrCTIPathOutsideTenant
	}
	if selectedID <= 0 {
		if err := ValidateCTIPath(nil, tenantID, requireFull, requireActive); err != nil {
			return nil, err
		}
		return nil, nil
	}
	path, err := resolveCTIPathNodes(ctx, tx, tenantID, selectedID, requireActive)
	if err != nil {
		return nil, err
	}
	if err := ValidateCTIPath(path, tenantID, requireFull, requireActive); err != nil {
		return nil, err
	}
	return path, nil
}

// GetCategoryPath 是只读投影：给读接口回显“完整路径”，不做任何写入或加锁。
// 需要与调用方同事务时使用 ProjectCTIPath，避免嵌套事务。
func (s *TicketCategoryService) GetCategoryPath(ctx context.Context, tenantID, selectedID int) ([]CTINode, error) {
	if s.client == nil {
		return nil, errors.New("CTI projection requires a client")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return s.ProjectCTIPath(ctx, tx, tenantID, selectedID)
}

// ProjectCTIPath 在调用方事务内解析只读路径投影（不加锁、不写入）。
func (s *TicketCategoryService) ProjectCTIPath(ctx context.Context, tx *ent.Tx, tenantID, selectedID int) ([]CTINode, error) {
	if tx == nil {
		return nil, errors.New("CTI projection requires the owning transaction")
	}
	if tenantID <= 0 {
		return nil, ErrCTIPathOutsideTenant
	}
	if selectedID <= 0 {
		return nil, nil
	}
	path, err := resolveCTIPathNodes(ctx, tx, tenantID, selectedID, false)
	if err != nil {
		return nil, err
	}
	if err := ValidateCTIPath(path, tenantID, false, false); err != nil {
		return nil, err
	}
	return path, nil
}

// resolveCTIPathNodes 从最深节点向上收集至多三级，并在需要时锁住整条路径。
func resolveCTIPathNodes(ctx context.Context, tx *ent.Tx, tenantID, selectedID int, lock bool) ([]CTINode, error) {
	chain := make([]CTINode, 0, CTIMaxDepth)
	current := selectedID
	for depth := 0; depth < CTIMaxDepth; depth++ {
		query := tx.TicketCategory.Query().Where(ticketcategory.IDEQ(current), ticketcategory.TenantIDEQ(tenantID))
		if lock {
			query = query.Where(ctiRowLock)
		}
		node, err := query.Only(ctx)
		if ent.IsNotFound(err) {
			if depth == 0 {
				return nil, ErrCTICategoryNotFound
			}
			return nil, ErrCTIPathHierarchy
		}
		if err != nil {
			return nil, err
		}
		chain = append(chain, ctiNodeOf(node))
		if node.ParentID == 0 {
			path := reverseCTIChain(chain)
			// 层级以真实父链为准：level 列是派生缓存，历史行的陈旧值不能让合法路径
			// 在创建/质量校验时被误判为不合法（迁移预检单独报告这类脏数据）。
			for index := range path {
				path[index].Level = index + 1
			}
			return path, nil
		}
		current = node.ParentID
	}
	// 走过三级仍未到达根：链更深，不能递归无限解析。
	return nil, ErrCTIPathTooDeep
}

func ctiNodeOf(category *ent.TicketCategory) CTINode {
	return CTINode{
		ID:       category.ID,
		ParentID: category.ParentID,
		Level:    category.Level,
		TenantID: category.TenantID,
		Name:     category.Name,
		Code:     category.Code,
		Active:   category.IsActive,
	}
}

func reverseCTIChain(chain []CTINode) []CTINode {
	path := make([]CTINode, len(chain))
	for index := range chain {
		path[len(chain)-1-index] = chain[index]
	}
	return path
}

// CreateCategory 创建工单分类。层级由父级推导，最多三级。
func (s *TicketCategoryService) CreateCategory(ctx context.Context, req *CreateCategoryRequest) (*ent.TicketCategory, error) {
	if req == nil {
		return nil, errors.New("分类请求不能为空")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Code = strings.TrimSpace(req.Code)
	if req.Name == "" {
		return nil, errors.New("分类名称不能为空")
	}
	if req.Code == "" {
		return nil, errors.New("分类代码不能为空")
	}
	if req.TenantID <= 0 {
		return nil, ErrCTIPathOutsideTenant
	}
	if req.ParentID < 0 {
		return nil, ErrCTIPathHierarchy
	}

	var created *ent.TicketCategory
	err := s.withTx(ctx, func(tx *ent.Tx) error {
		if err := ensureCTICodeAvailable(ctx, tx, req.TenantID, req.Code, 0); err != nil {
			return err
		}
		level := 1
		if req.ParentID > 0 {
			parent, err := lockTicketCategory(ctx, tx, req.TenantID, req.ParentID)
			if err != nil {
				return err
			}
			// 父链本身必须是合法的同租户连续路径，避免在损坏链上继续加深。
			// 层级取真实链长，不信任可能陈旧或超深的 level 列。
			parentPath, err := resolveCTIPathNodes(ctx, tx, req.TenantID, parent.ID, false)
			if err != nil {
				return err
			}
			if len(parentPath) >= CTIMaxDepth {
				return ErrCTIPathTooDeep
			}
			level = len(parentPath) + 1
		}

		create := tx.TicketCategory.Create().
			SetName(req.Name).
			SetDescription(req.Description).
			SetCode(req.Code).
			SetLevel(level).
			SetSortOrder(req.SortOrder).
			SetIsActive(req.IsActive).
			SetTenantID(req.TenantID)
		if req.ParentID > 0 {
			create.SetParentID(req.ParentID)
		}
		if req.DepartmentID != nil && *req.DepartmentID > 0 {
			create.SetDepartmentID(*req.DepartmentID)
		}
		record, err := create.Save(ctx)
		if err != nil {
			if ent.IsConstraintError(err) {
				return fmt.Errorf("分类代码已存在: %w", err)
			}
			return err
		}
		created = record
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// GetCategory 获取工单分类
func (s *TicketCategoryService) GetCategory(ctx context.Context, id int, tenantID int) (*ent.TicketCategory, error) {
	return s.client.TicketCategory.Query().
		Where(ticketcategory.ID(id), ticketcategory.TenantID(tenantID)).
		Only(ctx)
}

// GetCategoryByCode 按租户内的分类代码读取节点。导入按父代码建树时使用：
// 同一个租户内代码唯一，跨租户不参与匹配。
func (s *TicketCategoryService) GetCategoryByCode(ctx context.Context, tenantID int, code string) (*ent.TicketCategory, error) {
	code = strings.TrimSpace(code)
	if tenantID <= 0 || code == "" {
		return nil, ErrCTICategoryNotFound
	}
	record, err := s.client.TicketCategory.Query().
		Where(ticketcategory.TenantIDEQ(tenantID), ticketcategory.CodeEQ(code)).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrCTICategoryNotFound
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

// ListCategories 获取工单分类列表
func (s *TicketCategoryService) ListCategories(ctx context.Context, req *ListCategoriesRequest) ([]*ent.TicketCategory, int, error) {
	query := s.client.TicketCategory.Query()

	// 应用过滤条件
	if req.ParentID != nil {
		if *req.ParentID == 0 {
			// 获取顶级分类
			query = query.Where(ticketcategory.ParentIDIsNil())
		} else {
			query = query.Where(ticketcategory.ParentIDEQ(*req.ParentID))
		}
	}
	if req.Level > 0 {
		query = query.Where(ticketcategory.LevelEQ(req.Level))
	}
	if req.IsActive != nil {
		query = query.Where(ticketcategory.IsActive(*req.IsActive))
	}
	if req.TenantID > 0 {
		query = query.Where(ticketcategory.TenantID(req.TenantID))
	}

	// 获取总数
	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	// 应用分页和排序
	if req.Page > 0 && req.PageSize > 0 {
		offset := (req.Page - 1) * req.PageSize
		query = query.Offset(offset).Limit(req.PageSize)
	}

	// 默认按排序顺序和名称排序
	query = query.Order(ent.Asc(ticketcategory.FieldSortOrder), ent.Asc(ticketcategory.FieldName))

	categories, err := query.All(ctx)
	if err != nil {
		return nil, 0, err
	}

	return categories, total, nil
}

// GetCategoryTree 获取分类树结构。includeInactive 为真时包含停用节点（维护界面需要
// 显示状态并恢复）；为假时只返回启用节点（选择器/申请入口）。
func (s *TicketCategoryService) GetCategoryTree(ctx context.Context, tenantID int, includeInactive bool) ([]*CategoryTreeItem, error) {
	query := s.client.TicketCategory.Query().Where(ticketcategory.TenantID(tenantID))
	if !includeInactive {
		query = query.Where(ticketcategory.IsActive(true))
	}
	categories, err := query.
		Order(ent.Asc(ticketcategory.FieldSortOrder), ent.Asc(ticketcategory.FieldName), ent.Asc(ticketcategory.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	// 构建分类树。只从真正的根（ParentID == 0）出发按已加载的父子关系向下展开：
	// 祖先被过滤（例如停用）时该分支整体不可选，后代不会悄悄变成根节点，
	// 避免用户选到一条没有合法完整路径的“孤儿”节点。
	children := make(map[int][]*ent.TicketCategory, len(categories))
	parentOf := make(map[int]int, len(categories))
	nameOf := make(map[int]string, len(categories))
	for _, category := range categories {
		children[category.ParentID] = append(children[category.ParentID], category)
		parentOf[category.ID] = category.ParentID
		nameOf[category.ID] = category.Name
	}

	projects := make(map[int]*CategoryTreeItem, len(categories))
	var rootCategories []*CategoryTreeItem
	queue := append([]*ent.TicketCategory(nil), children[0]...)
	visited := make(map[int]struct{}, len(categories))
	for len(queue) > 0 {
		category := queue[0]
		queue = queue[1:]
		if _, seen := visited[category.ID]; seen {
			continue
		}
		visited[category.ID] = struct{}{}
		item := &CategoryTreeItem{
			ID:          category.ID,
			ParentID:    category.ParentID,
			Name:        category.Name,
			Description: category.Description,
			Code:        category.Code,
			Level:       category.Level,
			SortOrder:   category.SortOrder,
			IsActive:    category.IsActive,
			Children:    []*CategoryTreeItem{},
		}
		pathIDs, pathNames := ctiPathProjection(parentOf, nameOf, category.ID)
		item.PathIDs = pathIDs
		item.Path = strings.Join(pathNames, " / ")
		projects[category.ID] = item
		if parent, ok := projects[category.ParentID]; ok && category.ParentID != 0 {
			parent.Children = append(parent.Children, item)
		} else {
			rootCategories = append(rootCategories, item)
		}
		queue = append(queue, children[category.ID]...)
	}

	return rootCategories, nil
}

// ctiPathProjection 返回节点的根→自身路径（ID 与名称，含自身）。
// 只做父链回溯并最多回溯三级，损坏或过深的链不会无限循环。
func ctiPathProjection(parentOf map[int]int, nameOf map[int]string, id int) ([]int, []string) {
	ids := make([]int, 0, CTIMaxDepth)
	names := make([]string, 0, CTIMaxDepth)
	seen := map[int]struct{}{}
	current := id
	for depth := 0; depth < CTIMaxDepth; depth++ {
		if current <= 0 {
			break
		}
		if _, duplicate := seen[current]; duplicate {
			break
		}
		seen[current] = struct{}{}
		ids = append(ids, current)
		names = append(names, nameOf[current])
		parent := parentOf[current]
		if parent == 0 {
			break
		}
		current = parent
	}
	// 反转为根→自身
	for left, right := 0, len(ids)-1; left < right; left, right = left+1, right-1 {
		ids[left], ids[right] = ids[right], ids[left]
		names[left], names[right] = names[right], names[left]
	}
	return ids, names
}

// UpdateCategory 更新工单分类。
//
// 编码创建后不可修改；父级变更与移动使用同一套校验（三级上限、无环、引用保护）；
// 停用前必须确认没有已发布目录引用该节点或其下级。
func (s *TicketCategoryService) UpdateCategory(ctx context.Context, id int, req *UpdateCategoryRequest, tenantID int) (*ent.TicketCategory, error) {
	if req == nil {
		return nil, errors.New("分类请求不能为空")
	}
	var updated *ent.TicketCategory
	err := s.withTx(ctx, func(tx *ent.Tx) error {
		current, err := lockTicketCategory(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if req.Code != "" && strings.TrimSpace(req.Code) != current.Code {
			return ErrCTICategoryCodeImmutable
		}

		update := tx.TicketCategory.UpdateOneID(id).Where(ticketcategory.TenantID(tenantID))
		if req.Name != "" {
			update.SetName(strings.TrimSpace(req.Name))
		}
		if req.Description != "" {
			update.SetDescription(req.Description)
		}
		movedParent := false
		if req.ParentID != nil {
			newLevel, err := s.validateMove(ctx, tx, tenantID, current, *req.ParentID)
			if err != nil {
				return err
			}
			if *req.ParentID > 0 {
				update.SetParentID(*req.ParentID)
			} else {
				update.ClearParentID()
			}
			update.SetLevel(newLevel)
			movedParent = true
		}
		if req.SortOrder != nil {
			update.SetSortOrder(*req.SortOrder)
		}
		if req.IsActive != nil {
			if !*req.IsActive {
				subtree, _, err := s.loadSubtree(ctx, tx, tenantID, id)
				if err != nil {
					return err
				}
				references, err := countCTIReferences(ctx, tx, tenantID, subtree)
				if err != nil {
					return err
				}
				if references.PublishedCatalogBlocking() {
					return ErrCTICategoryPublishedCatalog
				}
			}
			update.SetIsActive(*req.IsActive)
		}
		if req.DepartmentID != nil {
			if *req.DepartmentID > 0 {
				update.SetDepartmentID(*req.DepartmentID)
			} else {
				update.ClearDepartmentID()
			}
		}

		update.SetUpdatedAt(time.Now())
		record, err := update.Save(ctx)
		if err != nil {
			return err
		}
		if movedParent {
			if err := s.refreshDescendantLevels(ctx, tx, record.ID, record.Level, tenantID); err != nil {
				return err
			}
		}
		updated = record
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteCategory 删除工单分类：仅允许无子节点且无任何业务/配置引用。
// 引用在提交事务内重新校验，不依赖先查后写。
func (s *TicketCategoryService) DeleteCategory(ctx context.Context, id int, tenantID int) error {
	return s.withTx(ctx, func(tx *ent.Tx) error {
		current, err := lockTicketCategory(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		children, err := tx.TicketCategory.Query().
			Where(ticketcategory.ParentIDEQ(id), ticketcategory.TenantIDEQ(tenantID)).
			Count(ctx)
		if err != nil {
			return err
		}
		if children > 0 {
			return ErrCTICategoryHasChildren
		}
		references, err := countCTIReferences(ctx, tx, tenantID, []CTINode{ctiNodeOf(current)})
		if err != nil {
			return err
		}
		if references.Blocking() {
			return ErrCTICategoryReferenced
		}
		return tx.TicketCategory.DeleteOneID(id).Where(ticketcategory.TenantIDEQ(tenantID)).Exec(ctx)
	})
}

// MoveCategory 移动分类到新的父级或排序位置。
//
// 被引用的节点及其祖先（即整棵被移动子树）不能直接移动；目标层级必须使最深的
// 后代仍不超过三级。移动与后代层级刷新在同一事务内原子完成。
func (s *TicketCategoryService) MoveCategory(ctx context.Context, id int, req *MoveCategoryRequest, tenantID int) (*ent.TicketCategory, error) {
	if req == nil {
		return nil, errors.New("移动请求不能为空")
	}
	var moved *ent.TicketCategory
	err := s.withTx(ctx, func(tx *ent.Tx) error {
		category, err := lockTicketCategory(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		subtree, depths, err := s.loadSubtree(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		references, err := countCTIReferences(ctx, tx, tenantID, subtree)
		if err != nil {
			return err
		}
		if references.Blocking() {
			return ErrCTICategoryReferenced
		}

		newParentID := category.ParentID
		newLevel := category.Level
		if req.NewParentID != nil {
			newParentID = *req.NewParentID
			newLevel = 1
			if newParentID > 0 {
				parentPath, err := resolveCTIPathNodes(ctx, tx, tenantID, newParentID, false)
				if err != nil {
					return err
				}
				if len(parentPath) >= CTIMaxDepth {
					return ErrCTIPathTooDeep
				}
				parent, err := lockTicketCategory(ctx, tx, tenantID, newParentID)
				if err != nil {
					return err
				}
				isDescendant, err := s.isDescendantCategory(ctx, tx, parent.ID, id, tenantID)
				if err != nil {
					return err
				}
				if isDescendant {
					return errors.New("不能将分类移动到其子分类下")
				}
				newLevel = len(parentPath) + 1
			}
			if height := subtreeHeight(depths, id); newLevel+height-1 > CTIMaxDepth {
				return ErrCTIPathTooDeep
			}
		}

		update := tx.TicketCategory.UpdateOneID(id).
			Where(ticketcategory.TenantID(tenantID)).
			SetLevel(newLevel).
			SetUpdatedAt(time.Now())
		if req.NewParentID != nil {
			if newParentID > 0 {
				update.SetParentID(newParentID)
			} else {
				update.ClearParentID()
			}
		}
		if req.NewSortOrder != nil {
			update.SetSortOrder(*req.NewSortOrder)
		}
		record, err := update.Save(ctx)
		if err != nil {
			return err
		}
		if newLevel != category.Level {
			if err := s.refreshDescendantLevels(ctx, tx, id, newLevel, tenantID); err != nil {
				return err
			}
		}
		moved = record
		return nil
	})
	if err != nil {
		return nil, err
	}
	return moved, nil
}

// validateMove 校验把 category 挂到 newParentID 下是否合法，返回新的层级。
func (s *TicketCategoryService) validateMove(ctx context.Context, tx *ent.Tx, tenantID int, category *ent.TicketCategory, newParentID int) (int, error) {
	if newParentID == category.ID {
		return 0, errors.New("不能将分类设置为自身的父分类")
	}
	subtree, depths, err := s.loadSubtree(ctx, tx, tenantID, category.ID)
	if err != nil {
		return 0, err
	}
	references, err := countCTIReferences(ctx, tx, tenantID, subtree)
	if err != nil {
		return 0, err
	}
	if references.Blocking() {
		return 0, ErrCTICategoryReferenced
	}
	if newParentID <= 0 {
		if height := subtreeHeight(depths, category.ID); height > CTIMaxDepth {
			return 0, ErrCTIPathTooDeep
		}
		return 1, nil
	}
	parentPath, err := resolveCTIPathNodes(ctx, tx, tenantID, newParentID, false)
	if err != nil {
		return 0, err
	}
	if len(parentPath) >= CTIMaxDepth {
		return 0, ErrCTIPathTooDeep
	}
	parent, err := lockTicketCategory(ctx, tx, tenantID, newParentID)
	if err != nil {
		return 0, err
	}
	isDescendant, err := s.isDescendantCategory(ctx, tx, parent.ID, category.ID, tenantID)
	if err != nil {
		return 0, err
	}
	if isDescendant {
		return 0, errors.New("不能将分类移动到其子分类下")
	}
	newLevel := len(parentPath) + 1
	if height := subtreeHeight(depths, category.ID); newLevel+height-1 > CTIMaxDepth {
		return 0, ErrCTIPathTooDeep
	}
	return newLevel, nil
}

// loadSubtree 读取并锁住以 rootID 为根的整棵子树，返回节点投影与相对深度。
//
// 锁顺序固定为“一次性按 id 升序锁定整组”，避免与其它维护事务交叉持锁。锁后重新计算，
// 若子树在读取与加锁之间扩大，则补齐锁定一次；仍不一致时明确冲突，绝不基于过期视图移动。
func (s *TicketCategoryService) loadSubtree(ctx context.Context, tx *ent.Tx, tenantID, rootID int) ([]CTINode, map[int]int, error) {
	locked := map[int]struct{}{rootID: {}}
	for attempt := 0; attempt < 3; attempt++ {
		all, err := tx.TicketCategory.Query().Where(ticketcategory.TenantIDEQ(tenantID)).All(ctx)
		if err != nil {
			return nil, nil, err
		}
		nodes, depths, ids := subtreeOf(all, rootID)
		if len(ids) == 0 {
			return nil, nil, ErrCTICategoryNotFound
		}
		extra := false
		for _, id := range ids {
			if _, ok := locked[id]; !ok {
				locked[id] = struct{}{}
				extra = true
			}
		}
		lockIDs := make([]int, 0, len(locked))
		for id := range locked {
			lockIDs = append(lockIDs, id)
		}
		if _, err := tx.TicketCategory.Query().
			Where(ticketcategory.IDIn(lockIDs...), ticketcategory.TenantIDEQ(tenantID)).
			Order(ent.Asc(ticketcategory.FieldID)).
			Where(ctiRowLock).
			All(ctx); err != nil {
			return nil, nil, err
		}
		if !extra {
			return nodes, depths, nil
		}
	}
	return nil, nil, errors.New("分类子树正在被并发修改，请刷新后重试")
}

// subtreeOf 在以 all 为视图的租户分类集合中计算 rootID 的子树。
func subtreeOf(all []*ent.TicketCategory, rootID int) ([]CTINode, map[int]int, []int) {
	children := make(map[int][]int, len(all))
	byID := make(map[int]*ent.TicketCategory, len(all))
	for _, category := range all {
		byID[category.ID] = category
		children[category.ParentID] = append(children[category.ParentID], category.ID)
	}
	if _, exists := byID[rootID]; !exists {
		return nil, nil, nil
	}
	nodes := make([]CTINode, 0, len(all))
	ids := make([]int, 0, len(all))
	depths := make(map[int]int, len(all))
	queue := []int{rootID}
	depth := 0
	visited := make(map[int]struct{}, len(all))
	for len(queue) > 0 {
		next := make([]int, 0, len(queue))
		for _, id := range queue {
			if _, seen := visited[id]; seen {
				continue
			}
			visited[id] = struct{}{}
			category, exists := byID[id]
			if !exists {
				continue
			}
			nodes = append(nodes, ctiNodeOf(category))
			ids = append(ids, id)
			depths[id] = depth
			next = append(next, children[id]...)
		}
		queue = next
		depth++
	}
	return nodes, depths, ids
}

// subtreeHeight 返回子树以 rootID 为根的最大高度（节点数）。
func subtreeHeight(depths map[int]int, rootID int) int {
	rootDepth, exists := depths[rootID]
	if !exists {
		return 1
	}
	height := 1
	for _, depth := range depths {
		if candidate := depth - rootDepth + 1; candidate > height {
			height = candidate
		}
	}
	return height
}

func lockTicketCategory(ctx context.Context, tx *ent.Tx, tenantID, id int) (*ent.TicketCategory, error) {
	if id <= 0 {
		return nil, ErrCTICategoryNotFound
	}
	record, err := tx.TicketCategory.Query().
		Where(ticketcategory.IDEQ(id), ticketcategory.TenantIDEQ(tenantID)).
		Where(ctiRowLock).
		Only(ctx)
	if ent.IsNotFound(err) {
		return nil, ErrCTICategoryNotFound
	}
	if err != nil {
		return nil, err
	}
	return record, nil
}

func ensureCTICodeAvailable(ctx context.Context, tx *ent.Tx, tenantID int, code string, excludeID int) error {
	query := tx.TicketCategory.Query().
		Where(ticketcategory.CodeEQ(code), ticketcategory.TenantIDEQ(tenantID))
	if excludeID > 0 {
		query = query.Where(ticketcategory.IDNEQ(excludeID))
	}
	exists, err := query.Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("分类代码已存在")
	}
	return nil
}

func (s *TicketCategoryService) isDescendantCategory(ctx context.Context, tx *ent.Tx, candidateID, ancestorID, tenantID int) (bool, error) {
	currentID := candidateID
	visited := map[int]bool{}
	for currentID > 0 {
		if currentID == ancestorID {
			return true, nil
		}
		if visited[currentID] {
			return false, errors.New("分类层级存在循环引用")
		}
		visited[currentID] = true

		category, err := tx.TicketCategory.Query().
			Where(ticketcategory.ID(currentID), ticketcategory.TenantID(tenantID)).
			Only(ctx)
		if err != nil {
			return false, err
		}
		currentID = category.ParentID
	}
	return false, nil
}

// refreshDescendantLevels 在调用方事务内按父级层级刷新整棵子树的 level。
func (s *TicketCategoryService) refreshDescendantLevels(ctx context.Context, tx *ent.Tx, parentID, parentLevel, tenantID int) error {
	queue := []struct {
		id    int
		level int
	}{{parentID, parentLevel}}
	visited := map[int]struct{}{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := tx.TicketCategory.Query().
			Where(ticketcategory.ParentIDEQ(current.id), ticketcategory.TenantIDEQ(tenantID)).
			All(ctx)
		if err != nil {
			return err
		}
		for _, child := range children {
			if _, seen := visited[child.ID]; seen {
				continue
			}
			visited[child.ID] = struct{}{}
			childLevel := current.level + 1
			if err := tx.TicketCategory.UpdateOneID(child.ID).
				Where(ticketcategory.TenantIDEQ(tenantID)).
				SetLevel(childLevel).
				SetUpdatedAt(time.Now()).
				Exec(ctx); err != nil {
				return err
			}
			queue = append(queue, struct {
				id    int
				level int
			}{child.ID, childLevel})
		}
	}
	return nil
}

// CreateCategoryRequest 创建分类请求
// TenantID 由控制器从认证上下文注入，不参与请求体校验
type CreateCategoryRequest struct {
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	Code         string `json:"code" binding:"required"`
	ParentID     int    `json:"parentId"`
	SortOrder    int    `json:"sortOrder"`
	IsActive     bool   `json:"isActive"`
	DepartmentID *int   `json:"departmentId"`
	TenantID     int    `json:"tenantId"`
}

// UpdateCategoryRequest 更新分类请求
type UpdateCategoryRequest struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Code         string `json:"code"`
	ParentID     *int   `json:"parentId"`
	SortOrder    *int   `json:"sortOrder"`
	IsActive     *bool  `json:"isActive"`
	DepartmentID *int   `json:"departmentId"`
}

// MoveCategoryRequest 移动分类请求
type MoveCategoryRequest struct {
	NewParentID  *int `json:"newParentId"`
	NewSortOrder *int `json:"newSortOrder"`
}

// ListCategoriesRequest 获取分类列表请求
type ListCategoriesRequest struct {
	Page     int   `json:"page" form:"page"`
	PageSize int   `json:"pageSize" form:"page_size"`
	ParentID *int  `json:"parentId" form:"parent_id"`
	Level    int   `json:"level" form:"level"`
	IsActive *bool `json:"isActive" form:"is_active"`
	TenantID int   `json:"tenantId" form:"tenant_id"`
}

// CategoryTreeItem 分类树项目。Path 为派生的根→自身完整路径（C/T/I 名称），
// 用于维护界面的路径搜索；工单仍只持久化最深节点。
type CategoryTreeItem struct {
	ID          int                 `json:"id"`
	ParentID    int                 `json:"parentId"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Code        string              `json:"code"`
	Level       int                 `json:"level"`
	SortOrder   int                 `json:"sortOrder"`
	IsActive    bool                `json:"isActive"`
	Path        string              `json:"path"`
	PathIDs     []int               `json:"pathIds"`
	Children    []*CategoryTreeItem `json:"children"`
}
