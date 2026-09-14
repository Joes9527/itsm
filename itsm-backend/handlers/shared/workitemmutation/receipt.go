package workitemmutation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"itsm-backend/ent"
	"itsm-backend/ent/auditlog"
)

// Digest hashes only normalized business input, never transport or secret data.
func Digest(command any) (string, error) {
	data, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Replay reads the immutable audit receipt. Authorization belongs to the owner
// and must precede this lookup, including on retry after a failed transaction.
func Replay(ctx context.Context, client *ent.Client, meta Meta, workItemID int, digest string) (Result, bool, error) {
	row, err := client.AuditLog.Query().Where(auditlog.TenantID(meta.TenantID), auditlog.UserID(meta.ActorID), auditlog.OperationID(meta.OperationID)).Only(ctx)
	if ent.IsNotFound(err) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if row.RequestDigest == nil || *row.RequestDigest != digest || row.Path != strconv.Itoa(workItemID) {
		return Result{}, false, &OperationConflictError{OperationID: meta.OperationID}
	}
	if row.ResultVersion == nil || row.ResultStatus == nil {
		return Result{}, false, fmt.Errorf("incomplete immutable operation receipt")
	}
	return Result{WorkItemID: workItemID, Version: *row.ResultVersion, Status: *row.ResultStatus, Replayed: true}, true, nil
}

type OperationConflictError struct{ OperationID string }

func (*OperationConflictError) Error() string {
	return "operationId already used for a different command"
}

// RecordTx stores the sole operation receipt alongside its domain mutation.
func RecordTx(ctx context.Context, tx *ent.Tx, meta Meta, result Result, action, digest string, facts any) error {
	data, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	_, err = tx.AuditLog.Create().SetTenantID(meta.TenantID).SetUserID(meta.ActorID).
		SetOperationID(meta.OperationID).SetRequestDigest(digest).SetResultVersion(result.Version).SetResultStatus(result.Status).
		SetResource("work_item").SetAction(action).SetPath(strconv.Itoa(result.WorkItemID)).SetMethod(meta.Source).SetRequestID(meta.CorrelationID).SetStatusCode(200).SetRequestBody(string(data)).Save(ctx)
	return err
}
