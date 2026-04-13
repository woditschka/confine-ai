package config

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestValidateMemory(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		// Valid formats.
		{name: "bytes lowercase", input: "1024b"},
		{name: "kilobytes lowercase", input: "512k"},
		{name: "megabytes lowercase", input: "256m"},
		{name: "gigabytes lowercase", input: "8g"},
		{name: "bytes uppercase", input: "1024B"},
		{name: "kilobytes uppercase", input: "512K"},
		{name: "megabytes uppercase", input: "256M"},
		{name: "gigabytes uppercase", input: "8G"},
		{name: "large number", input: "2048m"},

		// Invalid formats.
		{name: "no unit suffix", input: "1024", wantErr: "invalid memory format"},
		{name: "no number", input: "g", wantErr: "invalid memory format"},
		{name: "empty string", input: "", wantErr: "invalid memory format"},
		{name: "plain text", input: "invalid", wantErr: "invalid memory format"},
		{name: "negative number", input: "-1g", wantErr: "invalid memory format"},
		{name: "decimal number", input: "1.5g", wantErr: "invalid memory format"},
		{name: "spaces", input: "8 g", wantErr: "invalid memory format"},
		{name: "wrong unit", input: "8t", wantErr: "invalid memory format"},
		{name: "zero bytes", input: "0b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMemory(tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ValidateMemory(%q) = nil, want error containing %q", tt.input, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ValidateMemory(%q) error = %q, want containing %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("ValidateMemory(%q) unexpected error: %v", tt.input, err)
			}
		})
	}
}

func TestValidateCPUs(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		// Valid values.
		{name: "integer", input: "4"},
		{name: "decimal", input: "2.5"},
		{name: "half core", input: "0.5"},
		{name: "large number", input: "128"},

		// Invalid values.
		{name: "zero", input: "0", wantErr: "cpus must be a finite positive number"},
		{name: "negative", input: "-1", wantErr: "cpus must be a finite positive number"},
		{name: "negative decimal", input: "-0.5", wantErr: "cpus must be a finite positive number"},
		{name: "non-numeric", input: "abc", wantErr: "invalid cpus value"},
		{name: "empty string", input: "", wantErr: "invalid cpus value"},
		{name: "spaces", input: "4 ", wantErr: "invalid cpus value"},
		{name: "overflow to infinity", input: "1e309", wantErr: "invalid cpus value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCPUs(tt.input)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ValidateCPUs(%q) = nil, want error containing %q", tt.input, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ValidateCPUs(%q) error = %q, want containing %q", tt.input, err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("ValidateCPUs(%q) unexpected error: %v", tt.input, err)
			}
		})
	}
}

func TestResolveResourceLimits(t *testing.T) {
	tests := []struct {
		name      string
		cliMemory string
		cliCPUs   string
		cfg       *Customizations
		want      ResourceLimits
		wantErr   string
	}{
		// CLI precedence over config.
		{
			name:      "CLI memory overrides config",
			cliMemory: "8g",
			cfg:       &Customizations{Memory: "4g", CPUs: "2"},
			want:      ResourceLimits{Memory: "8g", CPUs: "2"},
		},
		{
			name:    "CLI cpus overrides config",
			cliCPUs: "8",
			cfg:     &Customizations{Memory: "4g", CPUs: "2"},
			want:    ResourceLimits{Memory: "4g", CPUs: "8"},
		},
		{
			name:      "CLI both override config",
			cliMemory: "16g",
			cliCPUs:   "8",
			cfg:       &Customizations{Memory: "4g", CPUs: "2"},
			want:      ResourceLimits{Memory: "16g", CPUs: "8"},
		},

		// Config only.
		{
			name: "config memory and cpus",
			cfg:  &Customizations{Memory: "4g", CPUs: "2"},
			want: ResourceLimits{Memory: "4g", CPUs: "2"},
		},

		// CLI only.
		{
			name:      "CLI memory only",
			cliMemory: "8g",
			want:      ResourceLimits{Memory: "8g"},
		},
		{
			name:    "CLI cpus only",
			cliCPUs: "4",
			want:    ResourceLimits{CPUs: "4"},
		},

		// Neither source.
		{
			name: "no limits from any source",
			want: ResourceLimits{},
		},

		// Nil config.
		{
			name: "nil config no CLI",
			cfg:  nil,
			want: ResourceLimits{},
		},
		{
			name:      "nil config with CLI",
			cliMemory: "8g",
			cliCPUs:   "4",
			cfg:       nil,
			want:      ResourceLimits{Memory: "8g", CPUs: "4"},
		},

		// Config with empty fields.
		{
			name: "config with empty memory",
			cfg:  &Customizations{CPUs: "2"},
			want: ResourceLimits{CPUs: "2"},
		},
		{
			name: "config with empty cpus",
			cfg:  &Customizations{Memory: "4g"},
			want: ResourceLimits{Memory: "4g"},
		},

		// Validation errors.
		{
			name:      "invalid CLI memory",
			cliMemory: "invalid",
			wantErr:   "invalid memory format",
		},
		{
			name:    "invalid CLI cpus",
			cliCPUs: "0",
			wantErr: "cpus must be a finite positive number",
		},
		{
			name:    "invalid config memory",
			cfg:     &Customizations{Memory: "bad"},
			wantErr: "invalid memory format",
		},
		{
			name:    "invalid config cpus",
			cfg:     &Customizations{CPUs: "-1"},
			wantErr: "cpus must be a finite positive number",
		},
		{
			name:      "CLI memory valid overrides invalid config memory",
			cliMemory: "8g",
			cfg:       &Customizations{Memory: "bad"},
			want:      ResourceLimits{Memory: "8g"},
		},

		// Independent resolution.
		{
			name:      "CLI memory does not affect config cpus",
			cliMemory: "8g",
			cfg:       &Customizations{CPUs: "4"},
			want:      ResourceLimits{Memory: "8g", CPUs: "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveResourceLimits(tt.cliMemory, tt.cliCPUs, tt.cfg)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("ResolveResourceLimits(%q, %q, %v) = %v, want error containing %q",
						tt.cliMemory, tt.cliCPUs, tt.cfg, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ResolveResourceLimits() error = %q, want containing %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("ResolveResourceLimits() unexpected error: %v", err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ResolveResourceLimits() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
