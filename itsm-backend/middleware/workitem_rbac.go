package middleware

// resourceForRecordClass 把 tickets.record_class 映射到 RBAC 资源名，供
// RequireWorkItemRecordClassPermission 使用。除 incident/problem/change_request 三个专业域外，
// 其余 record_class（generic/service_request_item/catalog_task，以及未来任何新值）统一映射到
// "ticket"——这三个是本设计新引入的专业资源名，其余都是 Ticket 自己的记录类型。
func resourceForRecordClass(recordClass string) string {
	switch recordClass {
	case "incident":
		return "incident"
	case "problem":
		return "problem"
	case "change_request":
		return "change"
	default:
		return "ticket"
	}
}
