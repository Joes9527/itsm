package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProvisionBucketUsesAuthenticatedSDKAndVerifiesBucket(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
			t.Error("missing SDK authentication")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	if err := provisionBucket(context.Background(), strings.TrimPrefix(server.URL, "http://"), "ci-fixture", hex.EncodeToString(secret[:]), "ci-attachments"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(calls, ","); got != "PUT /ci-attachments/,HEAD /ci-attachments/" {
		t.Fatalf("unexpected storage calls: %s", got)
	}
}

func TestProvisionBucketStopsOnCreateFailure(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	if err := provisionBucket(context.Background(), strings.TrimPrefix(server.URL, "http://"), "ci-fixture", hex.EncodeToString(secret[:]), "ci-attachments"); err == nil {
		t.Fatal("expected provisioning failure")
	}
	if calls != 1 {
		t.Fatal("must not verify a failed bucket creation")
	}
}

func TestProvisionBucketRequiresSuccessfulReadback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		t.Fatal(err)
	}
	if err := provisionBucket(context.Background(), strings.TrimPrefix(server.URL, "http://"), "ci-fixture", hex.EncodeToString(secret[:]), "ci-attachments"); err == nil || !strings.Contains(err.Error(), "absent after provisioning") {
		t.Fatalf("expected missing-bucket rejection, got %v", err)
	}
}
