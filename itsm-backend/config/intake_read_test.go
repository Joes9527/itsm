package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntakeReferencePageSizeConfig(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    int
		invalid bool
	}{{"", 50, false}, {"2", 2, false}, {"0", 0, true}, {"-1", 0, true}, {"oops", 0, true}} {
		c, err := loadIntakeReadConfig(func(string) string { return tc.raw })
		if tc.invalid {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.want, c.ReferencePageSize)
		}
	}
}
