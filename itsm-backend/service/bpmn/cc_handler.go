package bpmn

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/group"
	"itsm-backend/ent/role"
	"itsm-backend/ent/ticket"
	"itsm-backend/ent/ticketcc"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

// CCTaskHandler 抄送服务任务处理器
type CCTaskHandler struct {
	HandlerBase
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewCCTaskHandler 创建抄送处理器
func NewCCTaskHandler(client *ent.Client, logger *zap.SugaredLogger) *CCTaskHandler {
	return &CCTaskHandler{
		client: client,
		logger: logger,
	}
}

// GetTaskType 返回任务类型
func (h *CCTaskHandler) GetTaskType() string {
	return "cc_task"
}

// GetHandlerID 返回处理器标识
func (h *CCTaskHandler) GetHandlerID() string {
	return "cc_handler"
}

// Execute 执行抄送服务任务
func (h *CCTaskHandler) Execute(ctx context.Context, task *ent.ProcessTask, variables map[string]interface{}) (*dto.ServiceTaskResult, error) {
	// 获取参数
	ticketID := GetIntFromVars(variables, "ticket_id")
	ccType := GetStringFromVars(variables, "ccType")
	ccUserIds := GetStringFromVars(variables, "ccUserIds")
	ccGroupIds := GetStringFromVars(variables, "ccGroupIds")
	ccRoleIds := GetStringFromVars(variables, "ccRoleIds")
	ccVariable := GetStringFromVars(variables, "ccVariable")
	ccNotify := GetBoolFromVars(variables, "ccNotify", true)
	notifyChannels := parseNotifyChannels(GetStringFromVars(variables, "notifyChannels"))
	addedBy := GetIntFromVars(variables, "addedBy")
	tenantID, err := RequireTenantID(ctx, variables)
	if err != nil {
		return nil, err
	}

	if ticketID == 0 {
		return nil, fmt.Errorf("工单ID不能为空")
	}

	h.logger.Infow(
		"Executing CC task via BPMN",
		"ticket_id", ticketID,
		"cc_type", ccType,
		"tenant_id", tenantID,
	)

	tx, err := h.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启抄送任务事务失败")
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Client().Ticket.Query().Where(ticket.ID(ticketID), ticket.TenantID(tenantID)).Only(ctx); err != nil {
		return nil, fmt.Errorf("权威工单目标不存在")
	}

	// 解析获取最终的抄送人ID列表
	ccUsers, err := h.resolveCCUsers(ctx, tx.Client(), ccType, ccUserIds, ccGroupIds, ccRoleIds, ccVariable, variables, tenantID)
	if err != nil {
		h.logger.Errorw("Failed to resolve CC users", "error_class", "recipient_validation")
		return nil, err
	}

	if len(ccUsers) == 0 {
		h.logger.Warnw("No CC users resolved, skip CC task")
		return &dto.ServiceTaskResult{
			Success: true,
			Message: "没有解析到抄送人，跳过抄送任务",
			OutputVars: map[string]interface{}{
				"added_cc_users": []int{},
			},
		}, nil
	}

	// 添加抄送人
	var addedUsers []int
	for _, ccUserID := range ccUsers {
		// 检查是否已存在抄送记录
		exists, err := tx.Client().TicketCC.Query().
			Where(ticketcc.TicketID(ticketID),
				ticketcc.UserID(ccUserID),
				ticketcc.TenantID(tenantID),
				ticketcc.IsActive(true)).
			Exist(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查抄送关系失败")
		}
		if !exists {
			err = tx.Client().TicketCC.Create().
				SetTicketID(ticketID).
				SetUserID(ccUserID).
				SetAddedBy(addedBy).
				SetTenantID(tenantID).
				SetAddedAt(time.Now()).
				SetIsActive(true).
				Exec(ctx)
			if err != nil {
				return nil, fmt.Errorf("创建抄送关系失败")
			}
			addedUsers = append(addedUsers, ccUserID)
		}
	}

	// 发送通知给抄送人
	if ccNotify && len(addedUsers) > 0 {
		if err := h.createCCNotifications(ctx, tx.Client(), ticketID, addedUsers, notifyChannels, tenantID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交抄送任务事务失败")
	}

	return &dto.ServiceTaskResult{
		Success: true,
		Message: fmt.Sprintf("已成功添加 %d 位抄送人", len(addedUsers)),
		OutputVars: map[string]interface{}{
			"added_cc_users": addedUsers,
		},
	}, nil
}

// resolveCCUsers 解析抄送人ID列表
func (h *CCTaskHandler) resolveCCUsers(ctx context.Context, client *ent.Client, ccType, ccUserIds, ccGroupIds, ccRoleIds, ccVariable string, variables map[string]interface{}, tenantID int) ([]int, error) {
	switch ccType {
	case "user":
		ids, err := h.parseCommaSeparatedInts(ccUserIds)
		if err != nil {
			return nil, err
		}
		return h.validateCCUsers(ctx, client, ids, tenantID)
	case "group":
		groupIds, err := h.parseCommaSeparatedInts(ccGroupIds)
		if err != nil {
			return nil, err
		}
		return h.getUserIDsFromGroups(ctx, client, groupIds, tenantID)
	case "role":
		roleIds, err := h.parseCommaSeparatedInts(ccRoleIds)
		if err != nil {
			return nil, err
		}
		return h.getUserIDsFromRoles(ctx, client, roleIds, tenantID)
	case "variable":
		if ccVariable == "" {
			return nil, fmt.Errorf("动态变量名不能为空")
		}
		value, ok := variables[ccVariable]
		if !ok {
			return nil, fmt.Errorf("变量 %s 不存在", ccVariable)
		}
		// 处理不同类型的变量值
		switch v := value.(type) {
		case []interface{}:
			res := make([]int, 0, len(v))
			for _, item := range v {
				switch i := item.(type) {
				case float64:
					res = append(res, int(i))
				case int:
					res = append(res, i)
				case int64:
					res = append(res, int(i))
				case string:
					id, err := strconv.Atoi(i)
					if err != nil {
						return nil, fmt.Errorf("抄送用户ID格式无效")
					}
					res = append(res, id)
				}
			}
			return h.validateCCUsers(ctx, client, res, tenantID)
		case string:
			// 尝试用逗号分隔解析
			ids, err := h.parseCommaSeparatedInts(v)
			if err != nil {
				return nil, err
			}
			return h.validateCCUsers(ctx, client, ids, tenantID)
		case int:
			return h.validateCCUsers(ctx, client, []int{v}, tenantID)
		case float64:
			return h.validateCCUsers(ctx, client, []int{int(v)}, tenantID)
		default:
			return nil, fmt.Errorf("不支持的变量类型 %T", v)
		}
	default:
		// 默认按用户ID处理
		ids, err := h.parseCommaSeparatedInts(ccUserIds)
		if err != nil {
			return nil, err
		}
		return h.validateCCUsers(ctx, client, ids, tenantID)
	}
}

func (h *CCTaskHandler) validateCCUsers(ctx context.Context, client *ent.Client, ids []int, tenantID int) ([]int, error) {
	unique := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			unique[id] = struct{}{}
		}
	}
	if len(unique) == 0 {
		return []int{}, nil
	}
	requested := make([]int, 0, len(unique))
	for id := range unique {
		requested = append(requested, id)
	}
	resolved, err := client.User.Query().Where(
		user.IDIn(requested...), user.TenantID(tenantID), user.Active(true),
	).Select(user.FieldID).Ints(ctx)
	if err != nil || len(resolved) != len(requested) {
		return nil, fmt.Errorf("抄送用户不存在或不属于当前租户")
	}
	return resolved, nil
}

// parseCommaSeparatedInts 解析逗号分隔的整数列表
func (h *CCTaskHandler) parseCommaSeparatedInts(str string) ([]int, error) {
	if str == "" {
		return []int{}, nil
	}
	parts := strings.Split(str, ",")
	res := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// 处理变量占位符，如 ${applyUserId}
		if strings.HasPrefix(part, "${") && strings.HasSuffix(part, "}") {
			// 这里会在流程变量替换阶段处理，暂时保留原样，由上层替换
			// TODO: 支持变量解析
			h.logger.Warnw("Variable placeholder in CC user ID not supported yet", "placeholder", part)
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("抄送用户ID格式无效")
		}
		res = append(res, id)
	}
	return res, nil
}

// getUserIDsFromGroups 根据用户组ID获取用户ID列表
func (h *CCTaskHandler) getUserIDsFromGroups(ctx context.Context, client *ent.Client, groupIds []int, tenantID int) ([]int, error) {
	if len(groupIds) == 0 {
		return []int{}, nil
	}
	users, err := client.User.Query().
		Where(user.TenantID(tenantID), user.HasGroupsWith(group.IDIn(groupIds...))).
		Select(user.FieldID).
		Ints(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询用户组成员失败: %w", err)
	}
	return users, nil
}

// getUserIDsFromRoles 根据角色ID获取用户ID列表
func (h *CCTaskHandler) getUserIDsFromRoles(ctx context.Context, client *ent.Client, roleIds []int, tenantID int) ([]int, error) {
	if len(roleIds) == 0 {
		return []int{}, nil
	}
	users, err := client.User.Query().
		Where(user.TenantID(tenantID), user.HasRolesWith(role.IDIn(roleIds...))).
		Select(user.FieldID).
		Ints(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询角色成员失败: %w", err)
	}
	return users, nil
}

func parseNotifyChannels(value string) []string {
	if value == "" {
		return []string{"in_app"}
	}
	allowed := map[string]struct{}{
		"in_app":   {},
		"email":    {},
		"sms":      {},
		"feishu":   {},
		"dingtalk": {},
		"wecom":    {},
		"webhook":  {},
	}
	seen := map[string]struct{}{}
	channels := []string{}
	for _, part := range strings.Split(value, ",") {
		channel := strings.TrimSpace(part)
		if _, ok := allowed[channel]; !ok {
			continue
		}
		if _, exists := seen[channel]; exists {
			continue
		}
		seen[channel] = struct{}{}
		channels = append(channels, channel)
	}
	if len(channels) == 0 {
		return []string{"in_app"}
	}
	return channels
}

func (h *CCTaskHandler) createCCNotifications(ctx context.Context, client *ent.Client, ticketID int, userIDs []int, channels []string, tenantID int) error {
	ticketEntity, err := client.Ticket.Query().Where(ticket.ID(ticketID), ticket.TenantID(tenantID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("获取抄送通知工单失败")
	}
	now := time.Now()
	content := fmt.Sprintf("工单 %s「%s」已抄送给你", ticketEntity.TicketNumber, ticketEntity.Title)
	executionKey, hasExecutionKey := BPMNCallbackExecutionKey(ctx)
	for _, userID := range userIDs {
		for _, channel := range channels {
			create := client.TicketNotification.Create().
				SetTicketID(ticketID).
				SetUserID(userID).
				SetType("cc").
				SetChannel(channel).
				SetContent(content).
				SetTenantID(tenantID).
				SetStatus("pending")
			if channel == "in_app" {
				create.SetStatus("sent").SetSentAt(now)
			}
			if hasExecutionKey {
				create.SetDeliveryKey(executionKey)
			}
			if _, err := create.Save(ctx); err != nil {
				return fmt.Errorf("创建抄送通知失败")
			}
		}
		create := client.Notification.Create().
			SetTitle("工单抄送").
			SetMessage(content).
			SetType("info").
			SetUserID(userID).
			SetTenantID(tenantID).
			SetActionURL(fmt.Sprintf("/tickets/%d", ticketID)).
			SetActionText("查看工单")
		if hasExecutionKey {
			create.SetDeliveryKey(executionKey)
		}
		if _, err := create.Save(ctx); err != nil {
			return fmt.Errorf("创建统一抄送通知失败")
		}
	}
	return nil
}

// Validate 验证配置
func (h *CCTaskHandler) Validate(ctx context.Context, config map[string]interface{}) error {
	return nil
}

// 确保 CCTaskHandler 实现了 ServiceTaskHandlerInterface
var _ ServiceTaskHandlerInterface = (*CCTaskHandler)(nil)
