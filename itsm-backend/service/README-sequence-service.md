# WorkItem numbering

All professional records project their owning WorkItem `tickets.ticket_number`.
`repository/workitemnumber.PostgreSQLAllocator` assigns `TKT-YYYYMM-NNNNNN`
inside the Intake transaction, scoped by tenant. Incident has no separate number
sequence or stored number. Migration 027 validates legacy Incident number values
against WorkItem before removing the duplicate column.
