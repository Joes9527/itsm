package config

import (
	"fmt"
	"strconv"
)

type IntakeReadConfig struct{ ReferencePageSize int }

func loadIntakeReadConfig(getenv func(string) string) (IntakeReadConfig, error) {
	result := IntakeReadConfig{ReferencePageSize: 50}
	if raw := getenv("INTAKE_REFERENCE_PAGE_SIZE"); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 1 {
			return result, fmt.Errorf("INTAKE_REFERENCE_PAGE_SIZE must be a positive integer")
		}
		result.ReferencePageSize = size
	}
	return result, nil
}
