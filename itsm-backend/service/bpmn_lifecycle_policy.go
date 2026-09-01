package service

import (
	"fmt"
	"net/http"

	"itsm-backend/common"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
)

type BPMNProcessCommand string

const (
	BPMNProcessCommandSuspend   BPMNProcessCommand = "suspend"
	BPMNProcessCommandResume    BPMNProcessCommand = "resume"
	BPMNProcessCommandTerminate BPMNProcessCommand = "terminate"
)

type BPMNTaskCommand string

const (
	BPMNTaskCommandAssign            BPMNTaskCommand = "assign"
	BPMNTaskCommandClaim             BPMNTaskCommand = "claim"
	BPMNTaskCommandComplete          BPMNTaskCommand = "complete"
	BPMNTaskCommandCancel            BPMNTaskCommand = "cancel"
	BPMNTaskCommandDelegate          BPMNTaskCommand = "delegate"
	BPMNTaskCommandSetVariables      BPMNTaskCommand = "set_variables"
	BPMNTaskCommandCreateCounterSign BPMNTaskCommand = "create_counter_sign"
	BPMNTaskCommandVote              BPMNTaskCommand = "vote"
)

var bpmnProcessAllowedSourceStatuses = map[BPMNProcessCommand][]string{
	BPMNProcessCommandSuspend:   {"running"},
	BPMNProcessCommandResume:    {"suspended"},
	BPMNProcessCommandTerminate: {"running", "suspended"},
}

var bpmnTaskAllowedSourceStatuses = map[BPMNTaskCommand][]string{
	BPMNTaskCommandAssign: {
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	},
	BPMNTaskCommandClaim: {common.ProcessTaskStatusCreated},
	BPMNTaskCommandComplete: {
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	},
	BPMNTaskCommandCancel: {
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	},
	BPMNTaskCommandDelegate: {
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	},
	BPMNTaskCommandSetVariables: {
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	},
	BPMNTaskCommandCreateCounterSign: {
		common.ProcessTaskStatusCreated,
		common.ProcessTaskStatusAssigned,
		common.ProcessTaskStatusStarted,
		common.ProcessTaskStatusDelegated,
	},
	BPMNTaskCommandVote: {common.ProcessTaskStatusAssigned},
}

func ValidateBPMNProcessLifecycle(command BPMNProcessCommand, status string) error {
	allowedStatuses, err := bpmnProcessSourceStatuses(command)
	if err != nil {
		return err
	}
	if !bpmnLifecycleStatusAllowed(allowedStatuses, status) {
		return bpmnProcessLifecycleConflict(command)
	}
	return nil
}

func ValidateBPMNTaskLifecycle(command BPMNTaskCommand, status string) error {
	allowedStatuses, err := bpmnTaskSourceStatuses(command)
	if err != nil {
		return err
	}
	if !bpmnLifecycleStatusAllowed(allowedStatuses, status) {
		return bpmnTaskLifecycleConflict(command)
	}
	return nil
}

func bpmnProcessLifecyclePredicate(command BPMNProcessCommand, observedVersion int) (predicate.ProcessInstance, error) {
	allowedStatuses, err := bpmnProcessSourceStatuses(command)
	if err != nil {
		return nil, err
	}
	return processinstance.And(
		processinstance.StatusIn(allowedStatuses...),
		processinstance.VersionEQ(observedVersion),
	), nil
}

func bpmnTaskLifecyclePredicate(command BPMNTaskCommand, observedVersion int) (predicate.ProcessTask, error) {
	allowedStatuses, err := bpmnTaskSourceStatuses(command)
	if err != nil {
		return nil, err
	}
	return processtask.And(
		processtask.StatusIn(allowedStatuses...),
		processtask.AggregationVersionEQ(observedVersion),
	), nil
}

func bpmnProcessLifecycleConflict(command BPMNProcessCommand) error {
	return common.NewAppError(
		common.ErrCodeConflict,
		"BPMN process lifecycle conflict",
		http.StatusConflict,
		nil,
	).WithDetail(fmt.Sprintf("command %q is not allowed from the observed lifecycle state", command))
}

func bpmnTaskLifecycleConflict(command BPMNTaskCommand) error {
	return common.NewAppError(
		common.ErrCodeConflict,
		"BPMN task lifecycle conflict",
		http.StatusConflict,
		nil,
	).WithDetail(fmt.Sprintf("command %q is not allowed from the observed lifecycle state", command))
}

func bpmnProcessSourceStatuses(command BPMNProcessCommand) ([]string, error) {
	statuses, ok := bpmnProcessAllowedSourceStatuses[command]
	if !ok {
		return nil, common.NewValidationError(
			"unsupported BPMN process lifecycle command",
			fmt.Errorf("unknown command %q", command),
		)
	}
	return statuses, nil
}

func bpmnTaskSourceStatuses(command BPMNTaskCommand) ([]string, error) {
	statuses, ok := bpmnTaskAllowedSourceStatuses[command]
	if !ok {
		return nil, common.NewValidationError(
			"unsupported BPMN task lifecycle command",
			fmt.Errorf("unknown command %q", command),
		)
	}
	return statuses, nil
}

func bpmnLifecycleStatusAllowed(allowedStatuses []string, status string) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}
