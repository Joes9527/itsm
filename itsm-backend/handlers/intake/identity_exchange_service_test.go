package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/require"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type identityTestNonces struct {
	keys map[string]bool
	ttl  time.Duration
	err  error
}

func (n *identityTestNonces) Claim(_ context.Context, k string, ttl time.Duration) (bool, error) {
	n.ttl = ttl
	if n.err != nil {
		return false, n.err
	}
	if n.keys[k] {
		return false, nil
	}
	n.keys[k] = true
	return true, nil
}
func assertionTestSignature(a IdentityAssertion) string {
	m := hmac.New(sha256.New, []byte("test-only-key"))
	m.Write([]byte(strings.Join([]string{strconv.Itoa(a.Version), a.Audience, a.Purpose, a.Provider, a.Workspace, a.Subject, a.Channel, a.EventID, strconv.FormatInt(a.IssuedAt, 10), a.Nonce}, "\n")))
	return hex.EncodeToString(m.Sum(nil))
}
func assertionFixture() (*IdentityExchangeService, IdentityAssertion, *identityTestNonces) {
	n := &identityTestNonces{keys: map[string]bool{}}
	s := &IdentityExchangeService{config: IdentityExchangeConfig{Providers: map[string]IdentityProvider{"kaf": {Secret: "test-only-key", Channels: []string{"kaf_web"}, Purposes: []string{"create", "read"}}}, MaxAge: time.Minute, FutureSkew: 5 * time.Second, TokenTTL: 5 * time.Minute}, nonces: n, now: func() time.Time { return time.Unix(1788566400, 500000000) }}
	a := IdentityAssertion{Version: 2, Audience: "itsm-intake", Purpose: "create", Provider: "kaf", Workspace: "11111111-1111-4111-8111-111111111111", Subject: "subject-test", Channel: "kaf_web", EventID: "submission-test", IssuedAt: 1788566400, Nonce: "nonce-test"}
	a.Signature = assertionTestSignature(a)
	return s, a, n
}
func TestIdentityAssertionAcceptsV2AndRejectsReplayAcrossPurposes(t *testing.T) {
	s, a, n := assertionFixture()
	require.NoError(t, s.verify(context.Background(), a, "create"))
	require.Equal(t, time.Minute, n.ttl)
	require.Error(t, s.verify(context.Background(), a, "create"))
	a.Purpose = "read"
	a.Signature = assertionTestSignature(a)
	require.Error(t, s.verify(context.Background(), a, "read"))
}
func TestIdentityAssertionRejectsAmbiguityAndUntrustedFields(t *testing.T) {
	cases := map[string]func(*IdentityAssertion){"version": func(a *IdentityAssertion) { a.Version = 1 }, "audience": func(a *IdentityAssertion) { a.Audience = "other" }, "purpose": func(a *IdentityAssertion) { a.Purpose = "read" }, "provider": func(a *IdentityAssertion) { a.Provider = "other" }, "channel": func(a *IdentityAssertion) { a.Channel = "other" }, "newline": func(a *IdentityAssertion) { a.Subject = "sub\nject" }, "cr": func(a *IdentityAssertion) { a.EventID = "event\rtest" }, "space": func(a *IdentityAssertion) { a.Workspace = " workspace" }, "future": func(a *IdentityAssertion) { a.IssuedAt += 6 }, "deadline": func(a *IdentityAssertion) { a.IssuedAt -= 60 }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, a, n := assertionFixture()
			change(&a)
			a.Signature = assertionTestSignature(a)
			require.Error(t, s.verify(context.Background(), a, "create"))
			require.Empty(t, n.keys)
		})
	}
	s, a, n := assertionFixture()
	a.Subject = "forged"
	require.Error(t, s.verify(context.Background(), a, "create"))
	require.Empty(t, n.keys)
	s, a, n = assertionFixture()
	a.Signature = strings.ToUpper(a.Signature)
	require.Error(t, s.verify(context.Background(), a, "create"))
	require.Empty(t, n.keys)
}
func TestIdentityAssertionFutureTTLAndUnavailableNonce(t *testing.T) {
	s, a, n := assertionFixture()
	a.IssuedAt += 5
	a.Signature = assertionTestSignature(a)
	require.NoError(t, s.verify(context.Background(), a, "create"))
	require.Equal(t, 65*time.Second, n.ttl)
	s, a, n = assertionFixture()
	n.err = errors.New("redis unavailable")
	require.Error(t, s.verify(context.Background(), a, "create"))
}

func TestIdentityAssertionAllFieldsRejectCRLFAndOuterWhitespace(t *testing.T) {
	for field := 0; field < 8; field++ {
		for _, bad := range []string{"\r", "\n", " value", "value "} {
			s, a, n := assertionFixture()
			values := []*string{&a.Audience, &a.Purpose, &a.Provider, &a.Workspace, &a.Subject, &a.Channel, &a.EventID, &a.Nonce}
			*values[field] = bad
			a.Signature = assertionTestSignature(a)
			require.Error(t, s.verify(context.Background(), a, "create"))
			require.Empty(t, n.keys)
		}
	}
}

func TestIdentitySharedFixtureAcceptedAndExactDeadlineRejected(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/contracts/fixtures/intake-identity-signature.json")
	require.NoError(t, err)
	var fixture struct{ Signature string }
	require.NoError(t, json.Unmarshal(raw, &fixture))
	s, a, _ := assertionFixture()
	a.Signature = fixture.Signature
	require.NoError(t, s.verify(context.Background(), a, "create"))
	s, a, _ = assertionFixture()
	s.now = func() time.Time { return time.Unix(a.IssuedAt+60, 0) }
	require.Error(t, s.verify(context.Background(), a, "create"))
}
