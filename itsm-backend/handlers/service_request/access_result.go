package service_request

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"itsm-backend/handlers/common/accessgrant"
	"math"
	"strings"
	"time"
)

// ComputeAccessExpiry uses the first persisted verification, never a retry clock.
// Business duration limits belong to the approved catalog policy.
func ComputeAccessExpiry(verifiedAt time.Time, durationSeconds int64) (time.Time, error) {
	if verifiedAt.IsZero() || durationSeconds <= 0 || durationSeconds > math.MaxInt64/int64(time.Second) {
		return time.Time{}, fmt.Errorf("invalid finite access duration or verification time")
	}
	expiry := verifiedAt.UTC().Add(time.Duration(durationSeconds) * time.Second)
	if expiry.Year() > 9999 {
		return time.Time{}, fmt.Errorf("access expiry exceeds RFC3339 range")
	}
	return expiry, nil
}

// ValidateAccessResult validates the receipt contract only. C2 supplies task,
// actor, lease/action ledger and evidence provenance, then contributes this
// result to the existing atomic workflow completion transaction.
func ValidateAccessResult(raw []byte, snapshot accessgrant.ApprovalSnapshot) (*accessgrant.Result, *time.Time, error) {
	var result accessgrant.Result
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, nil, fmt.Errorf("access result must be one JSON object")
	}
	if result.Provider != accessgrant.Graph || result.Provider != snapshot.Provider || strings.TrimSpace(result.SubjectID) == "" || result.SubjectID != snapshot.SubjectID || strings.TrimSpace(result.GroupID) == "" || result.GroupID != snapshot.GroupID || strings.TrimSpace(result.EvidenceRef) == "" || result.VerifiedAt.IsZero() {
		return nil, nil, fmt.Errorf("access result does not match approved target or verification evidence")
	}
	expiry, err := ComputeAccessExpiry(result.VerifiedAt, snapshot.DurationSeconds)
	if err != nil {
		return nil, nil, err
	}
	result.VerifiedAt = result.VerifiedAt.UTC()
	switch {
	case result.Outcome == "granted" && result.Baseline == "not_member":
		return &result, &expiry, nil
	case result.Outcome == "already_present" && result.Baseline == "member":
		return &result, nil, nil
	default:
		return nil, nil, fmt.Errorf("unverified outcome or inconsistent membership baseline")
	}
}
