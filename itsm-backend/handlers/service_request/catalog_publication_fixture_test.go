package service_request_test

import (
	"context"
	"go.uber.org/zap"
	"itsm-backend/config"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/handlers/intake"
	"itsm-backend/handlers/service_catalog"
	sr "itsm-backend/handlers/service_request"
	"itsm-backend/service"
	"strconv"
	"time"
)

func catalogCreateInput(name, category, description string, days int, status string, ci, cloud int, fields []service.FieldDefinitionInput, key, serviceType string) dto.CreateServiceCatalogRequest {
	input := dto.CreateServiceCatalogRequest{Name: name, Category: category, Description: description, Status: status, CITypeID: ci, CloudServiceID: cloud, ProcessDefinitionKey: key, ServiceType: serviceType, TargetClass: "service_request_item", RequiresApproval: true}
	if days > 0 {
		input.DeliveryTime = strconv.Itoa(days)
	}
	if fields != nil {
		input.Fields = make([]map[string]interface{}, 0, len(fields))
		for _, f := range fields {
			input.Fields = append(input.Fields, map[string]interface{}{"name": f.Name, "label": f.Label, "type": f.FieldType, "required": f.Required, "options": f.Options})
		}
	}
	return input
}
func configureCatalogPublicationForTest(ctx context.Context, client *ent.Client, tenantID int, catalog *service_catalog.Service) {
	configureSRIntakeFixture(ctx, client, tenantID)
	logger := zap.NewNop().Sugar()
	registry := intake.NewCreatorRegistry()
	if err := registry.Register(sr.NewService(sr.NewEntRepository(client), client, logger, service.NewApprovalChainResolver(client, logger))); err != nil {
		panic(err)
	}
	catalog.SetCreatorRegistry(registry)
	engine := service.NewCustomProcessEngine(client, logger).(*service.CustomProcessEngine)
	engine.SetPublicationKAFConfig(&config.Config{KAFOutbox: config.KAFOutboxConfig{WebhookURL: "http://127.0.0.1:1", WebhookSecret: "fixture-only-unused", BatchSize: 1, PollInterval: time.Second, MaxAttempts: 1, HealthPort: 12345}})
	catalog.SetPublicationEngine(engine)
}
