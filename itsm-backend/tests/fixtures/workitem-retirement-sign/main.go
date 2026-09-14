// Test-only isolated authority. This fixture never connects to a database.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"itsm-backend/migration"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "key" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		json.NewEncoder(os.Stdout).Encode(map[string][]byte{"public": pub, "private": priv})
		return
	}
	var in struct {
		Evidence migration.MigrationEvidence
		Private  ed25519.PrivateKey
	}
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil {
		panic(err)
	}
	if len(in.Private) != ed25519.PrivateKeySize {
		panic("fixture private key required")
	}
	if err := migration.ValidateRetirementEvidence(in.Evidence); err != nil {
		panic(err)
	}
	digest, err := migration.RetirementEvidenceDigest(in.Evidence)
	if err != nil {
		panic(err)
	}
	e := &in.Evidence
	a := migration.RetirementAuthorization{KeyID: "isolated-task6", Action: string(migration.OpRetire), EvidenceDigest: digest, Target: e.Target, Operator: e.Operator, ChangeRecord: e.ChangeRecord, ExpiresAt: time.Now().UTC().Add(time.Hour)}
	b, err := migration.RetirementAuthorizationPayload(a)
	if err != nil {
		panic(err)
	}
	a.Signature = ed25519.Sign(in.Private, b)
	e.Retirement.Authorization = &a
	if err := json.NewEncoder(os.Stdout).Encode(e); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
