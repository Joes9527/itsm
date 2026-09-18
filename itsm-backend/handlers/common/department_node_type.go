package common

import "fmt"

// 组织节点类型。类型写在节点上，不靠"处在第几层"判断——
// 实测旧 ITIL 最深 14 层、eHR 最深 11 层，且"公司下面直接是部门"是常态。
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

// normalizeDepartmentNodeType 只接受四种已知取值。
//
// 未知取值 fail-closed：静默归类会让组织树长出无法解释的类型，
// 而"组织负责人"这类审批找人会顺着错误节点取到错误的人。
func normalizeDepartmentNodeType(raw string) (string, error) {
	if _, ok := departmentNodeTypes[raw]; !ok {
		return "", fmt.Errorf("unsupported department node type: %q", raw)
	}
	return raw, nil
}
