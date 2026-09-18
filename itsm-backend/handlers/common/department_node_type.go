package common

import "fmt"

// 组织节点类型。类型写在节点上，不靠"处在第几层"判断——
// 实测旧 ITIL 最深 14 层、eHR 最深 11 层，且"公司下面直接是部门"是常态。
//
// 空串表示**未分类**（canonical 050 的列默认值就是它）：历史数据尚未完成
// 业务分流，因此"未分类"必须是一个合法且可读的状态，而不是非法值。
const (
	orgNodeCompany    = "company"
	orgNodeBranch     = "branch"
	orgNodeDepartment = "department"
	orgNodeTeam       = "team"
)

var departmentNodeTypes = map[string]struct{}{
	orgNodeCompany:    {},
	orgNodeBranch:     {},
	orgNodeDepartment: {},
	orgNodeTeam:       {},
}

// NormalizeDepartmentNodeType 校验一个节点类型取值。
//
// 只接受四种已知取值与**精确的空串**（未分类）；其余一律 fail-closed：
// 静默归类会让组织树长出无法解释的类型，而"组织负责人"这类审批找人
// 会顺着错误节点取到错误的人。
//
// 特意**不做 trim**：`" company"` 这类带空格的取值在批量导入里是真实的数据质量
// 问题，规范化会把它掩盖掉；而数据库 CHECK 约束同样会拒绝它——两侧口径必须一致。
//
// 这是**唯一的**类型词汇表权威：部门的所有写入路径（创建、更新）都必须经过它。
func NormalizeDepartmentNodeType(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if _, ok := departmentNodeTypes[raw]; !ok {
		return "", fmt.Errorf("unsupported department node type: %q", raw)
	}
	return raw, nil
}
