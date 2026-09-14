// Package workitemcutover 实现 Wave 2 C1 的只读切换预检：判断 BPMN 业务身份能否从
// Wave-1 旧词表（ticket/change/service_request）切到规范 recordClass
// （generic/service_request_item/incident/problem/change_request/catalog_task）。
//
// 它只读：调用方必须在只读事务内调用 Inspect，本包不执行任何写入，也绝不取消、更新、
// 删除或重新触发实例。
//
// 历史记录不迁移：已结束的旧词表实例属于历史，只做信息性报告；阻塞切换的是仍然活跃的
// 旧依赖——运行/暂停的旧实例、待执行的旧回调、仍在生效的旧 binding——以及规范词表实例
// 自身无法与 WorkItem/专业扩展对上的身份不一致。
package workitemcutover

import (
	"context"
	"fmt"
	"sort"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/incident"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/processbinding"
	"itsm-backend/ent/processcallbackoutbox"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/ticket"
)

// SampleLimit bounds how many identifiers are reported per finding.
const SampleLimit = 20

// ScanLimit bounds how many rows are inspected. Truncation is NOT informational:
// Inspect fails closed on it (see inconclusive_truncated_scan), because a partial
// scan that happens to find nothing must never bless a cutover. Tests lower it to
// exercise that path without seeding ten thousand rows.
var ScanLimit = 10000

// ReleaseBusinessType keeps its existing explicit legacy identity. Release is not a
// WorkItem, so it is neither a convergence target nor a blocker.
const ReleaseBusinessType = "release"

// Finding is one observable preflight result: a count, non-sensitive identifiers and
// the reason it blocks (or the reason it is only informational).
type Finding struct {
	Kind      string `json:"kind"`
	Count     int    `json:"count"`
	Reason    string `json:"reason"`
	SampleIDs []int  `json:"sampleIds,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// CheckedTables reports the inspected row counts so operators can see the scope the
// answer was derived from.
type CheckedTables struct {
	ProcessInstances int `json:"processInstances"`
	CallbackOutbox   int `json:"callbackOutbox"`
	Bindings         int `json:"bindings"`
}

// Report is the immutable result of one preflight run.
type Report struct {
	TenantID      int           `json:"tenantId"`
	Switchable    bool          `json:"switchable"`
	Checked       CheckedTables `json:"checked"`
	Blockers      []Finding     `json:"blockers"`
	Informational []Finding     `json:"informational,omitempty"`
}

// Inspect evaluates the cutover gate. tenantID <= 0 inspects every tenant.
//
// The caller owns the transaction and must pass a read-only one; Inspect performs no
// writes and never mutates an instance, callback or binding.
func Inspect(ctx context.Context, tx *ent.Tx, tenantID int) (Report, error) {
	rep := Report{TenantID: tenantID, Blockers: []Finding{}, Informational: []Finding{}}
	canonical := dto.WorkItemRecordClasses()
	allowed := append(append([]string{}, canonical...), ReleaseBusinessType)
	canonicalSet := make(map[string]bool, len(canonical))
	for _, class := range canonical {
		canonicalSet[class] = true
	}

	instances := func() *ent.ProcessInstanceQuery {
		q := tx.ProcessInstance.Query()
		if tenantID > 0 {
			q = q.Where(processinstance.TenantID(tenantID))
		}
		return q
	}

	var err error
	if rep.Checked.ProcessInstances, err = instances().Count(ctx); err != nil {
		return rep, fmt.Errorf("count process instances: %w", err)
	}
	if rep.Checked.CallbackOutbox, err = callbackQuery(tx, tenantID).Count(ctx); err != nil {
		return rep, fmt.Errorf("count callback outbox rows: %w", err)
	}
	if rep.Checked.Bindings, err = bindingQuery(tx, tenantID).Count(ctx); err != nil {
		return rep, fmt.Errorf("count process bindings: %w", err)
	}

	// 1. Instances still carrying the Wave-1 vocabulary (or an unknown value).
	legacyInstances, err := instances().
		Where(nonCanonicalIdentity(allowed)).
		Order(ent.Asc(processinstance.FieldID)).
		Limit(ScanLimit+1).
		Select(processinstance.FieldID, processinstance.FieldStatus, processinstance.FieldBusinessType).
		All(ctx)
	if err != nil {
		return rep, fmt.Errorf("read non-canonical instances: %w", err)
	}
	truncated := false
	if len(legacyInstances) > ScanLimit {
		truncated = true
		legacyInstances = legacyInstances[:ScanLimit]
	}
	legacyIDs := make([]int, 0, len(legacyInstances))
	activeLegacyIDs := make([]int, 0, len(legacyInstances))
	historicalLegacyIDs := make([]int, 0, len(legacyInstances))
	for _, instance := range legacyInstances {
		legacyIDs = append(legacyIDs, instance.ID)
		if isActiveStatus(instance.Status) {
			activeLegacyIDs = append(activeLegacyIDs, instance.ID)
			continue
		}
		historicalLegacyIDs = append(historicalLegacyIDs, instance.ID)
	}
	if len(activeLegacyIDs) > 0 {
		rep.Blockers = append(rep.Blockers, Finding{
			Kind:      "active_legacy_instances",
			Count:     len(activeLegacyIDs),
			Reason:    "存在运行/暂停的旧词表流程实例：切换前必须先在旧运行时就地排空（不迁移、不取消）",
			SampleIDs: sample(activeLegacyIDs),
			Truncated: truncated,
		})
	}
	if len(historicalLegacyIDs) > 0 {
		rep.Informational = append(rep.Informational, Finding{
			Kind:      "historical_legacy_instances",
			Count:     len(historicalLegacyIDs),
			Reason:    "已结束的旧词表实例属于历史，不迁移也不阻塞；新运行时不解释其身份",
			SampleIDs: sample(historicalLegacyIDs),
			Truncated: truncated,
		})
	}

	// 2. A canonical string is not evidence: the instance must own the WorkItem and its
	// professional extension.
	mismatchedIDs, firstMismatch, mismatchTruncated, err := identityMismatches(ctx, tx, tenantID, canonicalSet)
	if err != nil {
		return rep, err
	}
	if len(mismatchedIDs) > 0 {
		rep.Blockers = append(rep.Blockers, Finding{
			Kind:      "identity_mismatch_instances",
			Count:     len(mismatchedIDs),
			Reason:    "规范词表的实例无法与 WorkItem/专业扩展对上：" + firstMismatch,
			SampleIDs: sample(mismatchedIDs),
			Truncated: mismatchTruncated,
		})
	}

	// 3. Callbacks that still have to run against a non-canonical instance.
	pendingIDs, pendingTruncated, err := pendingLegacyCallbacks(ctx, tx, tenantID, legacyIDs, truncated)
	if err != nil {
		return rep, err
	}
	if len(pendingIDs) > 0 {
		rep.Blockers = append(rep.Blockers, Finding{
			Kind:      "pending_legacy_callbacks",
			Count:     len(pendingIDs),
			Reason:    "仍有未完成的旧实例回调（pending/processing）：排空前不得切换，且不得自动重放或删除",
			SampleIDs: sample(pendingIDs),
			Truncated: pendingTruncated,
		})
	}

	// 4. Bindings still routed by the Wave-1 vocabulary.
	activeBindingIDs, bindingTruncated, err := activeLegacyBindings(ctx, tx, tenantID, allowed)
	if err != nil {
		return rep, err
	}
	if len(activeBindingIDs) > 0 {
		rep.Blockers = append(rep.Blockers, Finding{
			Kind:      "active_legacy_bindings",
			Count:     len(activeBindingIDs),
			Reason:    "仍有启用中的旧词表流程绑定：新词表下这些绑定不会被匹配，必须先迁移绑定配置",
			SampleIDs: sample(activeBindingIDs),
			Truncated: bindingTruncated,
		})
	}

	// 截断意味着预检没有看完全部数据：此时"没发现阻塞"不等于"没有阻塞"，
	// 必须失败关闭，否则会给出切换工具本应防止的虚假绿色结论。
	if truncated || mismatchTruncated || pendingTruncated || bindingTruncated {
		rep.Blockers = append(rep.Blockers, Finding{
			Kind:      "inconclusive_truncated_scan",
			Count:     1,
			Reason:    "预检扫描被行数上限截断，未能检查全部实例/回调/绑定：结论不可用，必须失败关闭（提高上限或缩小租户范围后重跑）",
			SampleIDs: nil,
			Truncated: true,
		})
	}

	sort.SliceStable(rep.Blockers, func(i, j int) bool { return rep.Blockers[i].Kind < rep.Blockers[j].Kind })
	rep.Switchable = len(rep.Blockers) == 0
	return rep, nil
}

// nonCanonicalIdentity matches instances whose business_type is a legacy or unknown
// value, including the unset column.
func nonCanonicalIdentity(allowed []string) predicate.ProcessInstance {
	return processinstance.Or(
		processinstance.BusinessTypeNotIn(allowed...),
		processinstance.BusinessTypeIsNil(),
	)
}

func identityMismatches(ctx context.Context, tx *ent.Tx, tenantID int, canonical map[string]bool) ([]int, string, bool, error) {
	q := tx.ProcessInstance.Query().
		Where(processinstance.BusinessTypeIn(dto.WorkItemRecordClasses()...)).
		Order(ent.Asc(processinstance.FieldID)).
		Limit(ScanLimit + 1)
	if tenantID > 0 {
		q = q.Where(processinstance.TenantID(tenantID))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, "", false, fmt.Errorf("read canonical instances: %w", err)
	}
	truncated := false
	if len(rows) > ScanLimit {
		truncated = true
		rows = rows[:ScanLimit]
	}

	ids := make([]int, 0)
	first := ""
	for _, instance := range rows {
		detail, ok, err := verifyInstanceIdentity(ctx, tx, instance, canonical)
		if err != nil {
			return nil, "", false, err
		}
		if ok {
			continue
		}
		ids = append(ids, instance.ID)
		if first == "" {
			first = fmt.Sprintf("instance %d: %s", instance.ID, detail)
		}
	}
	return ids, first, truncated, nil
}

// verifyInstanceIdentity checks that the instance key, the tenant-scoped WorkItem and
// the professional extension all agree on one canonical identity.
func verifyInstanceIdentity(ctx context.Context, tx *ent.Tx, instance *ent.ProcessInstance, canonical map[string]bool) (string, bool, error) {
	if !canonical[instance.BusinessType] {
		return "business_type 不是规范 WorkItem 词表", false, nil
	}
	expectedKey, err := dto.WorkItemBusinessKey(instance.BusinessType, instance.BusinessID)
	if err != nil {
		return "business_type/business_id 无法构成规范身份", false, nil
	}
	if instance.BusinessKey != expectedKey {
		return fmt.Sprintf("business_key=%q 与规范键 %q 不一致", instance.BusinessKey, expectedKey), false, nil
	}
	item, err := tx.Ticket.Query().
		Where(ticket.ID(instance.BusinessID), ticket.TenantID(instance.TenantID), ticket.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return "business_id 指向的 WorkItem 不存在或已删除", false, nil
		}
		return "", false, fmt.Errorf("read work item %d: %w", instance.BusinessID, err)
	}
	if item.RecordClass != instance.BusinessType {
		return fmt.Sprintf("WorkItem record_class=%s 与实例 business_type=%s 不一致", item.RecordClass, instance.BusinessType), false, nil
	}
	// The extension tables carry no tenant column; tenancy is already proven by the
	// WorkItem row above and each extension FK is unique to one WorkItem.
	switch instance.BusinessType {
	case dto.RecordClassIncident:
		exists, err := tx.Incident.Query().Where(incident.WorkItemID(item.ID)).Exist(ctx)
		if err != nil {
			return "", false, fmt.Errorf("read incident extension: %w", err)
		}
		if !exists {
			return "缺少 incident 专业扩展", false, nil
		}
	case dto.RecordClassProblem:
		exists, err := tx.Problem.Query().Where(problem.WorkItemID(item.ID)).Exist(ctx)
		if err != nil {
			return "", false, fmt.Errorf("read problem extension: %w", err)
		}
		if !exists {
			return "缺少 problem 专业扩展", false, nil
		}
	case dto.RecordClassChangeRequest:
		exists, err := tx.Change.Query().Where(change.WorkItemID(item.ID)).Exist(ctx)
		if err != nil {
			return "", false, fmt.Errorf("read change extension: %w", err)
		}
		if !exists {
			return "缺少 change 专业扩展", false, nil
		}
	case dto.RecordClassServiceRequestItem:
		exists, err := tx.ServiceRequest.Query().Where(servicerequest.TicketID(item.ID)).Exist(ctx)
		if err != nil {
			return "", false, fmt.Errorf("read service request extension: %w", err)
		}
		if !exists {
			return "缺少 service request 专业扩展", false, nil
		}
	}
	return "", true, nil
}

func pendingLegacyCallbacks(ctx context.Context, tx *ent.Tx, tenantID int, legacyInstanceIDs []int, truncatedInput bool) ([]int, bool, error) {
	if len(legacyInstanceIDs) == 0 {
		return nil, truncatedInput, nil
	}
	rows, err := callbackQuery(tx, tenantID).
		Where(
			processcallbackoutbox.StatusIn("pending", "processing"),
			processcallbackoutbox.ProcessInstanceIDIn(legacyInstanceIDs...),
		).
		Order(ent.Asc(processcallbackoutbox.FieldID)).
		Limit(ScanLimit + 1).
		Select(processcallbackoutbox.FieldID).
		All(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("read pending callbacks: %w", err)
	}
	truncated := truncatedInput
	if len(rows) > ScanLimit {
		truncated = true
		rows = rows[:ScanLimit]
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, truncated, nil
}

func activeLegacyBindings(ctx context.Context, tx *ent.Tx, tenantID int, allowed []string) ([]int, bool, error) {
	rows, err := bindingQuery(tx, tenantID).
		Where(processbinding.IsActive(true), processbinding.BusinessTypeNotIn(allowed...)).
		Order(ent.Asc(processbinding.FieldID)).
		Limit(ScanLimit + 1).
		Select(processbinding.FieldID).
		All(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("read active bindings: %w", err)
	}
	truncated := false
	if len(rows) > ScanLimit {
		truncated = true
		rows = rows[:ScanLimit]
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, truncated, nil
}

func callbackQuery(tx *ent.Tx, tenantID int) *ent.ProcessCallbackOutboxQuery {
	q := tx.ProcessCallbackOutbox.Query()
	if tenantID > 0 {
		q = q.Where(processcallbackoutbox.TenantID(tenantID))
	}
	return q
}

func bindingQuery(tx *ent.Tx, tenantID int) *ent.ProcessBindingQuery {
	q := tx.ProcessBinding.Query()
	if tenantID > 0 {
		q = q.Where(processbinding.TenantID(tenantID))
	}
	return q
}

func isActiveStatus(status string) bool {
	return status == "running" || status == "suspended"
}

func sample(ids []int) []int {
	if len(ids) <= SampleLimit {
		return ids
	}
	return ids[:SampleLimit]
}
