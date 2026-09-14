package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type descriptionProbe struct {
	calls  *[3]int
	digest string
	err    error
}

func (p *descriptionProbe) Manifest() Manifest {
	return Manifest{Name: "describe-probe", Version: "1", Provider: "probe", RequiredPermissions: []string{"connector:write"}}
}
func (p *descriptionProbe) Init(context.Context, Config) error   { p.calls[1]++; return nil }
func (p *descriptionProbe) Send(context.Context, *Message) error { p.calls[2]++; return nil }
func (p *descriptionProbe) HealthCheck(context.Context) HealthStatus {
	p.calls[2]++
	return HealthStatus{}
}
func (p *descriptionProbe) Close() error { p.calls[2]++; return nil }
func (p *descriptionProbe) DescribeDeliveryDestination(Config) (string, error) {
	return p.digest, p.err
}

type opaqueDescriptionProbe struct{ Connector }

func TestRegistryDescribesDestinationWithoutActivation(t *testing.T) {
	calls := [3]int{}
	registry := NewRegistry()
	registry.Register(func() Connector { calls[0]++; return &descriptionProbe{calls: &calls, digest: strings.Repeat("a", 64)} })
	describe, ok := any(registry).(interface{ DescribeDeliveryDestination(Config) (string, error) })
	if !ok {
		t.Fatal("registry lacks pure destination description")
	}
	for i := 0; i < 2; i++ {
		digest, err := describe.DescribeDeliveryDestination(Config{Name: "describe-probe", Provider: "probe", TenantID: 1, Enabled: false})
		if err != nil || digest != strings.Repeat("a", 64) {
			t.Fatalf("description failed: %q %v", digest, err)
		}
	}
	if calls != [3]int{1, 0, 0} {
		t.Fatalf("description activated provider: %v", calls)
	}
}

func TestRegistryDestinationDescriptionFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name, provider, digest string
		opaque                 bool
		err                    error
	}{
		{name: "missing", provider: "probe", digest: strings.Repeat("a", 64)},
		{name: "describe-probe", provider: "wrong", digest: strings.Repeat("a", 64)},
		{name: "describe-probe", provider: "probe", opaque: true},
		{name: "describe-probe", provider: "probe", digest: ""},
		{name: "describe-probe", provider: "probe", digest: strings.Repeat("A", 64)},
		{name: "describe-probe", provider: "probe", digest: strings.Repeat("a", 64), err: errors.New("secret endpoint detail")},
	} {
		t.Run(tc.name+tc.provider+tc.digest, func(t *testing.T) {
			calls := [3]int{}
			registry := NewRegistry()
			registry.Register(func() Connector {
				calls[0]++
				p := &descriptionProbe{calls: &calls, digest: tc.digest, err: tc.err}
				if tc.opaque {
					return &opaqueDescriptionProbe{Connector: p}
				}
				return p
			})
			describe, ok := any(registry).(interface{ DescribeDeliveryDestination(Config) (string, error) })
			if !ok {
				t.Fatal("registry lacks pure destination description")
			}
			digest, err := describe.DescribeDeliveryDestination(Config{Name: tc.name, Provider: tc.provider, TenantID: 1})
			if err == nil || digest != "" {
				t.Fatalf("invalid description accepted: %q %v", digest, err)
			}
			if strings.Contains(err.Error(), "secret endpoint detail") {
				t.Fatal("provider error leaked")
			}
			if calls != [3]int{1, 0, 0} {
				t.Fatalf("rejection activated provider: %v", calls)
			}
		})
	}
}
