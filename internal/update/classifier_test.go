package update

import (
	"errors"
	"strings"
	"testing"
)

func TestClassify_GoTarget(t *testing.T) {
	// AC-1: a tool=go managed group classifies as a Go target with the
	// "latest stable, no prompt" policy.
	pd := ParseDockerfile(readFixture(t, "valid.Dockerfile"))

	targets, err := Classify(pd)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}

	var gotGo *UpdateTarget
	for i := range targets {
		if targets[i].Tool == "go" {
			gotGo = &targets[i]
			break
		}
	}
	if gotGo == nil {
		t.Fatal("no go UpdateTarget produced")
		return // unreachable; satisfies staticcheck SA5011
	}
	if gotGo.PromptOnMajorJump {
		t.Errorf("go target PromptOnMajorJump = true, want false")
	}
	if gotGo.CurrentVersion != "1.26.0" {
		t.Errorf("go current version = %q, want %q", gotGo.CurrentVersion, "1.26.0")
	}
	// Arches must be discovered from the group's sha256 markers.
	if len(gotGo.Arches) != 2 {
		t.Errorf("go target Arches = %v, want 2 entries", gotGo.Arches)
	}
}

func TestClassify_CorrettoTarget(t *testing.T) {
	// AC-6: tool=java distribution=corretto classifies with prompt-on-major.
	pd := ParseDockerfile(readFixture(t, "valid.Dockerfile"))

	targets, err := Classify(pd)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}

	var gotJava *UpdateTarget
	for i := range targets {
		if targets[i].Tool == "java" {
			gotJava = &targets[i]
			break
		}
	}
	if gotJava == nil {
		t.Fatal("no java UpdateTarget produced")
		return // unreachable; satisfies staticcheck SA5011
	}
	if gotJava.Distribution != "corretto" {
		t.Errorf("java distribution = %q, want %q", gotJava.Distribution, "corretto")
	}
	if !gotJava.PromptOnMajorJump {
		t.Errorf("java/corretto PromptOnMajorJump = false, want true")
	}
	if gotJava.CurrentVersion != "25.0.2.10.1" {
		t.Errorf("corretto current version = %q, want %q", gotJava.CurrentVersion, "25.0.2.10.1")
	}
}

func TestClassify_UnknownDistribution(t *testing.T) {
	// AC-11: distribution=temurin is an error, not a skip.
	pd := ParseDockerfile(readFixture(t, "unknown-distribution.Dockerfile"))

	_, err := Classify(pd)
	if err == nil {
		t.Fatal("Classify() error = nil, want unknown-distribution error")
	}
	if !errors.Is(err, ErrUnknownDistribution) {
		t.Errorf("Classify() error = %v, want ErrUnknownDistribution", err)
	}
	if !strings.Contains(err.Error(), "temurin") {
		t.Errorf("error %q does not name the unknown distribution", err.Error())
	}
}

func TestClassify_MissingDistribution(t *testing.T) {
	// AC-12: tool=java kind=version without distribution= is an error.
	pd := ParseDockerfile(readFixture(t, "missing-distribution.Dockerfile"))

	_, err := Classify(pd)
	if err == nil {
		t.Fatal("Classify() error = nil, want missing-distribution error")
	}
	if !errors.Is(err, ErrMissingDistribution) {
		t.Errorf("Classify() error = %v, want ErrMissingDistribution", err)
	}
}

func TestClassify_BaseImageIgnored(t *testing.T) {
	// Base-image markers are observed but never produce an UpdateTarget.
	// The valid fixture has a base-image image marker on the FROM line;
	// classification returns only the go and java targets.
	pd := ParseDockerfile(readFixture(t, "valid.Dockerfile"))

	targets, err := Classify(pd)
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	for _, tgt := range targets {
		if tgt.Tool == "base-image" {
			t.Errorf("base-image target emitted: %+v", tgt)
		}
	}
}
