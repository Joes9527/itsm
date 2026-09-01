# Frontend UI Audit Inventory

Generated at: 2026-09-01T15:01:27.216Z

## Coverage

- Total pages: 142
- Batch 1 pages: 48
- Batch 2 pages: 30
- Batch 3 pages: 64

## Highest Complexity Pages

| Path | Batch | Type | Lines | Issues |
| --- | --- | --- | ---: | ---: |
| `profile/page.tsx` | batch-3 | list | 989 | 3 |
| `admin/service-catalogs/page.tsx` | batch-3 | admin | 934 | 2 |
| `tickets/templates/page.tsx` | batch-1 | list | 908 | 2 |
| `tickets/prototype/page.tsx` | batch-1 | list | 874 | 1 |
| `admin/workflows/page.tsx` | batch-3 | admin | 873 | 3 |
| `admin/users/page.tsx` | batch-3 | admin | 843 | 3 |
| `notifications/page.tsx` | batch-3 | list | 821 | 2 |
| `tickets/create/page.tsx` | batch-1 | create | 750 | 4 |
| `admin/escalation-rules/page.tsx` | batch-3 | admin | 744 | 2 |
| `workflow/instances/page.tsx` | batch-1 | list | 734 | 3 |
| `admin/sla-definitions/page.tsx` | batch-3 | admin | 725 | 2 |
| `admin/permissions/page.tsx` | batch-3 | admin | 720 | 2 |
| `admin/cmdb-types/page.tsx` | batch-3 | admin | 718 | 2 |
| `admin/roles/page.tsx` | batch-3 | admin | 677 | 3 |
| `dashboard/page.tsx` | batch-3 | list | 670 | 2 |

## Highest Complexity workflow/cmdb Components

| Path | Domain | Lines | Issues |
| --- | --- | ---: | ---: |
| `src/components/workflow/designer/WorkflowNodeInspector.tsx` | workflow | 1683 | 2 |
| `src/components/workflow/BPMNDesigner.tsx` | workflow | 1289 | 4 |
| `src/components/workflow/designer/WorkflowDesigner.tsx` | workflow | 1069 | 3 |
| `src/components/workflow/designer/WorkflowAIModal.tsx` | workflow | 665 | 2 |
| `src/components/cmdb/CSDMHub.tsx` | cmdb | 613 | 2 |
| `src/components/cmdb/TopologyGraph.tsx` | cmdb | 537 | 2 |
| `src/components/cmdb/CIRelationshipManager.tsx` | cmdb | 480 | 0 |
| `src/components/cmdb/CIList.tsx` | cmdb | 435 | 1 |
| `src/components/cmdb/CIEditorForm.tsx` | cmdb | 400 | 0 |
| `src/components/workflow/designer/WorkflowProperties.tsx` | workflow | 343 | 0 |
| `src/components/workflow/designer/WorkflowContext.tsx` | workflow | 244 | 0 |
| `src/components/workflow/__tests__/WorkflowNodeInspector.test.tsx` | workflow | 224 | 0 |
| `src/components/cmdb/ci-editor-shared.ts` | cmdb | 215 | 0 |
| `src/components/workflow/designer/WorkflowToolbar.tsx` | workflow | 197 | 0 |
| `src/components/cmdb/ci-detail/sections/CIImpactAnalysisTab.tsx` | cmdb | 189 | 0 |

## Notes

- Full machine-readable audit ledger lives in `docs/ui-audit/inventory.json`.
- Severity and issue categories are heuristic seeds for manual review, not final product decisions.
