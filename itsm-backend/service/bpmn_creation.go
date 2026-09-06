package service

import (
	"context"
	"errors"
	"go.uber.org/zap"
	"itsm-backend/ent"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/user"
	creation "itsm-backend/handlers/common/workitemcreation"
	"regexp"
	"strconv"
	"strings"
)

// ResolveCreationWorkflow freezes the owning process service's creation binding.
// A catalog key takes precedence. Only configured conditions.no_process permits
// skipping orchestration; absent/unsupported configurations fail closed.
func (s *ProcessBindingService) ResolveCreationWorkflow(ctx context.Context, tx *ent.Tx, plan *creation.CreationPlan, key string) (creation.ResolvedWorkflowBinding, *int, error) {
	var result creation.ResolvedWorkflowBinding
	if plan == nil {
		return result, nil, creation.NewInternalFailure("prepared creation plan is required for routing", nil)
	}
	in := plan.Resolved
	var slaID *int
	version := 0
	key = strings.TrimSpace(key)
	if key == "" {
		business := map[string]string{"generic": "ticket", "incident": "incident", "problem": "problem", "change_request": "change", "service_request_item": "service_request"}[in.RecordClass]
		if business == "" {
			return result, nil, creation.NewUnsupportedRecordClass("unsupported workflow creation class", nil)
		}
		subtype := plan.BusinessSubtype
		requester, err := tx.User.Query().Where(user.IDEQ(in.Identity.RequesterID), user.TenantIDEQ(in.Identity.TenantID), user.ActiveEQ(true)).Only(ctx)
		if ent.IsNotFound(err) {
			return result, nil, creation.NewReferenceNotFound("workflow requester is unavailable", err)
		}
		if err != nil {
			return result, nil, creation.NewInfrastructureUnavailable("could not load workflow requester", err)
		}
		variables := map[string]interface{}{}
		for key, value := range in.Command.FormValues {
			variables[key] = value
		}
		for key, value := range plan.RoutingValues {
			variables[key] = value
		}
		variables["priority"] = plan.WorkItem.Priority
		routing := &RoutingContext{TenantID: in.Identity.TenantID, BusinessType: business, BusinessSubType: subtype, DepartmentID: requester.DepartmentID, Category: in.CTI.CategoryName, Variables: variables}
		if in.CTI.CategoryID != nil {
			routing.CategoryID = *in.CTI.CategoryID
		}
		selected, err := NewProcessRoutingService(tx.Client(), zap.NewNop().Sugar()).FindBestRouteTx(ctx, tx, routing)
		if err != nil {
			var configurationError *RoutingConfigurationError
			if errors.As(err, &configurationError) {
				return result, nil, creation.NewDomainValidationFailed("invalid workflow routing configuration", err)
			}
			return result, nil, creation.NewInfrastructureUnavailable("could not select configured workflow", err)
		}
		if selected == nil {
			return result, nil, creation.NewWorkflowBindingRequired("active workflow binding is required", nil)
		}
		if selected.SLAPolicyID != "" {
			id, err := strconv.Atoi(selected.SLAPolicyID)
			if err != nil || id <= 0 {
				return result, nil, creation.NewDomainValidationFailed("invalid workflow SLA policy", err)
			}
			slaID = &id
		}
		if selected.NoProcess {
			return creation.ResolvedWorkflowBinding{NoProcess: true}, slaID, nil
		}
		key, version = selected.ProcessDefinitionKey, selected.ProcessVersion
	}
	query := tx.ProcessDefinition.Query().Where(processdefinition.TenantIDEQ(in.Identity.TenantID), processdefinition.KeyEQ(key), processdefinition.IsActiveEQ(true))
	if version <= 0 {
		query = query.Where(processdefinition.IsLatestEQ(true))
	}
	definitions, err := query.Order(ent.Desc(processdefinition.FieldIsLatest), ent.Desc(processdefinition.FieldDeployedAt), ent.Desc(processdefinition.FieldID)).All(ctx)
	if err != nil {
		return result, nil, creation.NewInfrastructureUnavailable("could not resolve workflow definition", err)
	}
	var definition *ent.ProcessDefinition
	for _, candidate := range definitions {
		major, err := creationProcessDefinitionMajorVersion(candidate.Version)
		if err != nil {
			return result, nil, creation.NewDomainValidationFailed("unsupported workflow definition version", err)
		}
		if version <= 0 || major == version {
			definition = candidate
			break
		}
	}
	if definition == nil {
		return result, nil, creation.NewWorkflowBindingRequired("active workflow definition for the configured major version is required", nil)
	}
	return creation.ResolvedWorkflowBinding{DefinitionID: &definition.ID, DefinitionKey: definition.Key, DefinitionVersion: definition.Version, DefinitionDigest: FreezeProcessDefinition(definition).Digest}, slaID, nil
}

// ProcessBinding stores a major-version reference. Definition snapshots retain
// the exact selected version; supported stored versions are integer, dotted
// numeric (up to three components), and their conventional v-prefixed forms.
var creationProcessVersionPattern = regexp.MustCompile(`^v?([1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){0,2}$`)

func creationProcessDefinitionMajorVersion(version string) (int, error) {
	matches := creationProcessVersionPattern.FindStringSubmatch(strings.TrimSpace(version))
	if matches == nil {
		return 0, errors.New("workflow version must be a positive numeric major or dotted numeric version")
	}
	return strconv.Atoi(matches[1])
}
