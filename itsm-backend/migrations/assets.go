// Package migrations embeds operational SQL assets for the canonical runtime registry.
package migrations

import _ "embed"

// WorkItemSLACycleSQL is also runnable as the reviewed standalone migration.
//
//go:embed 20260910_workitem_sla_cycle.sql
var WorkItemSLACycleSQL string

//go:embed 20260910_problem_investigation_completion.sql
var ProblemInvestigationCompletionSQL string
