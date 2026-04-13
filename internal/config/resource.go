package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// memoryPattern validates Docker memory format: digits followed by a unit
// suffix (b, k, m, g). Case-insensitive.
var memoryPattern = regexp.MustCompile(`(?i)^[0-9]+[bkmg]$`)

// ResourceLimits holds validated resource limit values. Produced by
// ResolveResourceLimits. Passed through UpOptions to createContainer.
type ResourceLimits struct {
	Memory string // Docker memory format, e.g., "8g". Empty means no limit.
	CPUs   string // Decimal CPU count, e.g., "4". Empty means no limit.
}

// ValidateMemory checks that v matches Docker's memory format: one or more
// digits followed by a unit suffix (b, k, m, g). Case-insensitive.
func ValidateMemory(v string) error {
	if !memoryPattern.MatchString(v) {
		return fmt.Errorf("invalid memory format %q: expected digits followed by b, k, m, or g (e.g., 8g, 512m)", v)
	}
	return nil
}

// ValidateCPUs checks that v is a positive decimal number.
func ValidateCPUs(v string) error {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("invalid cpus value %q: %w", v, err)
	}
	if f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Errorf("cpus must be a finite positive number, got %q", v)
	}
	return nil
}

// ResolveResourceLimits merges CLI flags with config values. CLI flags take
// precedence over configuration. Validates all non-empty values. Returns an
// error if any provided value fails validation.
func ResolveResourceLimits(cliMemory, cliCPUs string, cfg *Customizations) (ResourceLimits, error) {
	var limits ResourceLimits

	// Resolve memory: CLI > config > empty.
	switch {
	case cliMemory != "":
		limits.Memory = cliMemory
	case cfg != nil && cfg.Memory != "":
		limits.Memory = cfg.Memory
	}

	// Resolve CPUs: CLI > config > empty.
	switch {
	case cliCPUs != "":
		limits.CPUs = cliCPUs
	case cfg != nil && cfg.CPUs != "":
		limits.CPUs = cfg.CPUs
	}

	// Validate non-empty values.
	if limits.Memory != "" {
		if err := ValidateMemory(limits.Memory); err != nil {
			return ResourceLimits{}, err
		}
	}
	if limits.CPUs != "" {
		if err := ValidateCPUs(limits.CPUs); err != nil {
			return ResourceLimits{}, err
		}
	}

	return limits, nil
}
