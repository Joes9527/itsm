package main

import (
	"context"

	"itsm-backend/ent"
	"itsm-backend/service"
)

// managerLink 是一条"把 selfID 的上级设为 managerID"的待回填意图。
type managerLink struct {
	SelfID    int
	ManagerID int
}

// linkManagers 逐条校验并回填上级，返回被跳过的条数。
//
// 非法值（自引用 / 跨租户 / 上级非在职 / 上级不存在）一律**跳过并计数**，
// 绝不让一次导入因为历史脏数据整批失败——数据里已有自引用，
// 若遇到非法值就中断，导入将永远跑不完。
//
// 校验复用 service.ValidateUserManager：这是汇报线的唯一权威，
// 不在这里复制一份实现，否则导入与 API 会各说一套。
func linkManagers(ctx context.Context, client *ent.Client, tenantID int, links map[string]managerLink) (int, error) {
	skipped := 0
	for _, link := range links {
		if link.SelfID == 0 || link.ManagerID == 0 {
			continue
		}
		if err := service.ValidateUserManager(ctx, client, tenantID, link.SelfID, link.ManagerID); err != nil {
			skipped++
			continue
		}
		if err := client.User.UpdateOneID(link.SelfID).SetManagerID(link.ManagerID).Exec(ctx); err != nil {
			// 本人不存在（映射过期）也按跳过处理：这是脏数据而不是系统性故障，
			// 不能让一行脏数据把整批导入打断。其它错误照旧上报。
			if ent.IsNotFound(err) {
				skipped++
				continue
			}
			return skipped, err
		}
	}
	return skipped, nil
}
