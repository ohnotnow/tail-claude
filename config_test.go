package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestConfigResolveDefaults(t *testing.T) {
	brake, doom, th := Config{}.resolve()
	if th != defaultThreshold {
		t.Errorf("threshold = %d, want default %d", th, defaultThreshold)
	}
	if len(brake) != len(defaultBrakeWords) {
		t.Errorf("brake = %d words, want default %d", len(brake), len(defaultBrakeWords))
	}
	if len(doom) != len(defaultDoomWords) {
		t.Errorf("doom = %d words, want default %d", len(doom), len(defaultDoomWords))
	}
}

func TestConfigResolveOverrides(t *testing.T) {
	c := Config{BrakeWords: []string{"foo", "bar"}, Threshold: 3}
	brake, doom, th := c.resolve()

	if len(brake) != 2 || brake[0] != "foo" {
		t.Errorf("brake override failed: %v", brake)
	}
	if th != 3 {
		t.Errorf("threshold override = %d, want 3", th)
	}
	// doom was not set, so it must keep the default.
	if len(doom) != len(defaultDoomWords) {
		t.Errorf("doom should be default when unset, got %d words", len(doom))
	}
}

func TestConfigYAMLUnmarshal(t *testing.T) {
	data := "brake_words:\n  - wait\n  - hmm\ndrought_threshold: 4\n"
	var c Config
	if err := yaml.Unmarshal([]byte(data), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(c.BrakeWords) != 2 || c.BrakeWords[1] != "hmm" {
		t.Errorf("brake_words = %v", c.BrakeWords)
	}
	if c.Threshold != 4 {
		t.Errorf("drought_threshold = %d, want 4", c.Threshold)
	}
}
