package bpmn

// AuthPrimitive names which authorization function gates a route.
type AuthPrimitive string

const (
	// AuthPrimitiveTaskViewer: authorizeTaskViewer (participant/initiator/elevated read access to one task).
	AuthPrimitiveTaskViewer AuthPrimitive = "task_viewer"
	// AuthPrimitiveTaskMutation: authorizeTaskMutation (participant/elevated write access to one task).
	AuthPrimitiveTaskMutation AuthPrimitive = "task_mutation"
	// AuthPrimitiveCounterSignViewer: authorizeCounterSignViewer (parent-task-or-any-sub-task participant/elevated read access to counter-sign status).
	AuthPrimitiveCounterSignViewer AuthPrimitive = "counter_sign_viewer"
	// AuthPrimitiveTaskActor: authorizeTaskActor / isTaskCandidate (candidate-level check for claim/complete/decisions/vote).
	AuthPrimitiveTaskActor AuthPrimitive = "task_actor"
	// AuthPrimitiveParticipantScoped: ListUserTasks / ListProcessInstances forcing non-elevated callers to their own scope.
	AuthPrimitiveParticipantScoped AuthPrimitive = "participant_scoped"
	// AuthPrimitiveInstanceViewer: authorizeProcessInstanceViewer (participant/initiator/elevated read access to one process instance).
	AuthPrimitiveInstanceViewer AuthPrimitive = "instance_viewer"
	// AuthPrimitiveInstanceMutation: authorizeProcessInstanceMutation (initiator/elevated write access to one process instance's lifecycle).
	AuthPrimitiveInstanceMutation AuthPrimitive = "instance_mutation"
	// AuthPrimitiveCoarseRoleGate: middleware.RequireLegacyBPMNRoles() at the router-group
	// level (see controller/bpmn_workflow_controller.go's "managed" group). This is the
	// pre-existing, coarse 7-role whitelist gate that predates this plan's fine-grained
	// task/instance authorization work — it is not one of the authorizeXxx service-layer
	// functions Tasks 1-6 built. It applies to routes that create a new resource (no
	// existing task/instance to check participant/initiator ownership against), so there is
	// no elevation concept to layer on top. Included here only so the registry stays a
	// complete, accurate map of every /bpmn/tasks/* and /bpmn/process-instances/* route, per
	// the design spec's "every route needs an entry" requirement — not a claim that this
	// primitive was touched by this plan.
	AuthPrimitiveCoarseRoleGate AuthPrimitive = "coarse_role_gate"
)

// RouteAuthEntry documents which authorization primitive, elevated
// resource:action pair, and system-caller policy applies to one route.
// This is the single source of truth this plan's guard test
// (controller/bpmn_workflow_controller_authz_registry_test.go) checks
// against the actual gin route registration — a route with no entry here
// fails that test, preventing a repeat of the GetCounterSignStatus gap
// (shipped with zero authorization because no such list existed).
type RouteAuthEntry struct {
	Method            string // "GET" / "POST" / "PUT" / "DELETE"
	Path              string // gin route pattern relative to /api/v1, e.g. "/bpmn/tasks/:id"
	Primitive         AuthPrimitive
	ElevatedResource  string // empty string means this route has no elevation concept
	ElevatedAction    string
	AllowSystemCaller bool // whether BPMNSystemCallerContextKey is expected to be honored here
}

// BPMNTaskInstanceAuthRegistry lists every /bpmn/tasks/* and
// /bpmn/process-instances/* route and the authorization primitive that
// gates it. AllowSystemCaller is false throughout: as of 2026-08-26, a
// full-codebase audit found zero real non-HTTP, non-test callers of any of
// the service methods these routes reach — see the design spec's Component
// 2 for the audit method and conclusion. If a real system-caller use case
// is added later, flip the relevant row to true and record why.
// StartProcess (POST /bpmn/process-instances) is deliberately included even
// though it predates this plan's fine-grained task/instance authorization
// work (Tasks 1-6): the guard test walks every registered route under these
// two prefixes, and this route matches. It has no participant/initiator
// ownership concept to check (it creates a brand-new instance), so it is
// gated only by the pre-existing coarse RequireLegacyBPMNRoles role
// whitelist at the router-group level — see AuthPrimitiveCoarseRoleGate.
var BPMNTaskInstanceAuthRegistry = []RouteAuthEntry{
	{Method: "GET", Path: "/bpmn/tasks", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/tasks/:id", Primitive: AuthPrimitiveTaskViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/assign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/claim", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/complete", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "POST", Path: "/bpmn/tasks/:id/decisions", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/cancel", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/variables", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "POST", Path: "/bpmn/tasks/:id/counter-sign", Primitive: AuthPrimitiveTaskMutation, ElevatedResource: "task", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/tasks/:id/counter-sign-status", Primitive: AuthPrimitiveCounterSignViewer, ElevatedResource: "task", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/tasks/:id/vote", Primitive: AuthPrimitiveTaskActor, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "POST", Path: "/bpmn/process-instances", Primitive: AuthPrimitiveCoarseRoleGate, ElevatedResource: "", ElevatedAction: "", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/process-instances", Primitive: AuthPrimitiveParticipantScoped, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/process-instances/:id", Primitive: AuthPrimitiveInstanceViewer, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "GET", Path: "/bpmn/process-instances/:id/approval-history", Primitive: AuthPrimitiveInstanceViewer, ElevatedResource: "process_instance", ElevatedAction: "read", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/variables", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/suspend", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/resume", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
	{Method: "PUT", Path: "/bpmn/process-instances/:id/terminate", Primitive: AuthPrimitiveInstanceMutation, ElevatedResource: "process_instance", ElevatedAction: "update", AllowSystemCaller: false},
}
