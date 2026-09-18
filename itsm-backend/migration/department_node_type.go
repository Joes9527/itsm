package migration

// DepartmentNodeTypeVersion 让每个组织节点自带类型（公司/分公司/部门/组）。
//
// 类型是节点属性，不是层级推导出来的：实测组织树既不是五层整齐，同一类型
// 也出现在不同深度。SQL 资产由 migrations 包嵌入。
const DepartmentNodeTypeVersion = "050_department_node_type"
