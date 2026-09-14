package config

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestConnectorTargetUsesExistingEnvironmentResolution(t *testing.T) {
	t.Setenv("ITSM_TEST_TARGET_SECRET", "synthetic-resolved-secret")
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(`execution:
  connector_targets:
    - credentials:
        secret: "${ITSM_TEST_TARGET_SECRET}"
      settings:
        nested:
          - ["${ITSM_TEST_TARGET_SECRET}"]
`)))
	var raw map[string]interface{}
	require.NoError(t, v.Unmarshal(&raw))
	resolveMapEnvVars(raw)
	v.Set("execution", raw["execution"])
	var cfg Config
	require.NoError(t, v.Unmarshal(&cfg))
	require.Len(t, cfg.Execution.ConnectorTargets, 1)
	target := cfg.Execution.ConnectorTargets[0]
	require.Equal(t, "synthetic-resolved-secret", target.Credentials["secret"])
	require.Equal(t, "synthetic-resolved-secret", target.Settings["nested"].([]interface{})[0].([]interface{})[0])
}

func TestExecutionConnectorTargetsRejectUntrustedDeclarations(t *testing.T) {
	for _, scenario := range []string{"valid", "notification", "outbox", "foreign tenant", "foreign scope", "duplicate instance", "missing digest", "invalid digest", "empty provider", "ambiguous name", "unknown capability", "diagnostic capability", "disabled delivery", "missing delivery", "empty capabilities", "duplicate capability", "standard mode", "lossy settings"} {
		t.Run(scenario, func(t *testing.T) {
			target := map[string]interface{}{
				"tenant_id": 1, "scope_id": "149ff1af-a27c-47c7-827f-103271130bb9",
				"name": "webhook", "provider": "local-test",
				"destination_digest": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				"capabilities":       []string{"webhook"}, "settings": map[string]interface{}{"url": "http://127.0.0.1:12345"},
			}
			v := viper.New()
			v.Set("mode", "candidate")
			v.Set("deployment_id", "target-test")
			v.Set("scopes", []map[string]interface{}{{"tenant_id": 1, "scope_id": target["scope_id"]}})
			v.Set("capabilities", map[string]string{"webhook": "scoped"})
			targets := []map[string]interface{}{target}
			switch scenario {
			case "notification", "outbox":
				target["capabilities"] = []string{scenario}
				v.Set("capabilities", map[string]string{scenario: "scoped"})
			case "foreign tenant":
				target["tenant_id"] = 2
			case "foreign scope":
				target["scope_id"] = "249ff1af-a27c-47c7-827f-103271130bb9"
			case "duplicate instance":
				targets = append(targets, target)
			case "missing digest":
				delete(target, "destination_digest")
			case "invalid digest":
				target["destination_digest"] = "not-a-digest"
			case "empty provider":
				target["provider"] = ""
			case "ambiguous name":
				target["name"] = "webhook/other"
			case "unknown capability":
				target["capabilities"] = []string{"unknown"}
			case "diagnostic capability":
				target["capabilities"] = []string{"connector_diagnostics"}
			case "disabled delivery":
				v.Set("capabilities", map[string]string{"webhook": "disabled"})
			case "missing delivery":
				v.Set("capabilities", map[string]string{})
			case "lossy settings":
				target["settings"] = map[string]interface{}{"numeric_identity": int64(9007199254740993)}
			case "empty capabilities":
				target["capabilities"] = []string{}
			case "duplicate capability":
				target["capabilities"] = []string{"webhook", "webhook"}
			case "standard mode":
				v.Set("mode", "standard")
				v.Set("scopes", []map[string]interface{}{})
				v.Set("capabilities", map[string]string{"webhook": "enabled"})
			}
			v.Set("connector_targets", targets)
			var cfg ExecutionConfig
			require.NoError(t, v.Unmarshal(&cfg))
			if scenario == "valid" || scenario == "notification" || scenario == "outbox" || scenario == "disabled delivery" || scenario == "missing delivery" {
				require.NoError(t, cfg.Validate())
			} else {
				cfg.ConnectorTargets[0].Credentials = map[string]string{"secret": "synthetic-private-value"}
				err := cfg.Validate()
				require.Error(t, err)
				require.NotContains(t, err.Error(), "synthetic-private-value")
			}
		})
	}
}
