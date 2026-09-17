package service_catalog

import (
	"context"

	"itsm-backend/ent"
	"itsm-backend/ent/servicecatalog"
	"itsm-backend/service"
)

// CTIReferenceNameSource 由服务目录域实现：分类详情需要展示"哪些目录引用了该分类"，
// 名称由目录所有者提供，分类聚合层不直接读取本域数据。
type CTIReferenceNameSource struct{}

func (CTIReferenceNameSource) Kind() string { return "catalog" }

func (CTIReferenceNameSource) Resource() string { return "service_catalog" }

func (CTIReferenceNameSource) Names(ctx context.Context, client *ent.Client, tenantID int, ids []int) (map[int]string, error) {
	if client == nil || len(ids) == 0 {
		return map[int]string{}, nil
	}
	rows, err := client.ServiceCatalog.Query().
		Where(servicecatalog.TenantIDEQ(tenantID), servicecatalog.IDIn(ids...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(rows))
	for _, row := range rows {
		names[row.ID] = row.Name
	}
	return names, nil
}

// RegisterCTIReferenceNameSource 在启动装配时把本域的名称契约注册到分类聚合层。
func RegisterCTIReferenceNameSource() {
	service.RegisterCTIReferenceNameSource(CTIReferenceNameSource{})
}
