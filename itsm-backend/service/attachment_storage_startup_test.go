package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttachmentStartupDoesNotCreateMissingBucket(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			writes.Add(1)
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	_, err := NewMinioAttachmentStorage(strings.TrimPrefix(server.URL, "http://"), "test-access", "test-secret", "candidate-uploads", false)
	require.Error(t, err)
	require.Zero(t, writes.Load(), "startup must not create storage resources")
}
