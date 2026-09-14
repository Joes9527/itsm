package migration

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"
)

// LoadControlConfiguration reads deployment-owned policy independently of caller
// evidence. Operational identity is always derived from the actual OS account.
func LoadControlConfiguration() (MigrationControlConfig, error) {
	var c MigrationControlConfig
	filename := strings.TrimSpace(os.Getenv("ITSM_MIGRATION_CONTROL_FILE"))
	if filename == "" {
		return c, nil
	}
	f, err := os.Open(filename)
	if err != nil {
		return c, fmt.Errorf("cannot read trusted control file")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return c, fmt.Errorf("trusted control file must be regular and not group/world writable")
	}
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid trusted control configuration")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("trailing trusted configuration content")
	}
	if strings.TrimSpace(c.DeploymentID) == "" {
		return c, fmt.Errorf("trusted deployment identity is required")
	}
	u, err := user.Current()
	if err != nil {
		return c, fmt.Errorf("cannot derive operational identity")
	}
	c.Operator = u.Username
	return c, nil
}
