package authorization

var mspRoleToRBACRoleMap = map[string]string{
	"provider_admin": "msp_manager",
	"provider_agent": "msp_tech",
	"customer_user":  "end_user",
}

func GetMSPRBACRole(mspRole string) string {
	if rbacRole, ok := mspRoleToRBACRoleMap[mspRole]; ok {
		return rbacRole
	}
	return "msp_viewer"
}
