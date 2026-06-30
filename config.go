package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the optional user config. Every field is optional: tc ships with
// sensible baked-in defaults and is fully useful with no config file at all.
//
// A provided list REPLACES the corresponding default (override semantics). To
// start from the defaults and tweak, copy the shipped config.yaml.example —
// it contains the full default lists ready to edit.
type Config struct {
	BrakeWords []string `yaml:"brake_words"`
	DoomWords  []string `yaml:"doom_words"`
	Threshold  int      `yaml:"drought_threshold"`
}

// configPath returns ~/.config/tc/config.yaml (or the OS equivalent).
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "tc", "config.yaml"), nil
}

// loadConfig reads the config file if present. A missing file is not an error —
// it returns the zero Config, which resolves to the baked-in defaults.
func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, nil // no config dir; fall back to defaults silently
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	return c, nil
}

// resolve folds a Config over the baked-in defaults. A non-empty list or a
// positive threshold overrides the default; anything left unset keeps it.
func (c Config) resolve() (brake, doom []string, threshold int) {
	brake, doom, threshold = defaultBrakeWords, defaultDoomWords, defaultThreshold
	if len(c.BrakeWords) > 0 {
		brake = c.BrakeWords
	}
	if len(c.DoomWords) > 0 {
		doom = c.DoomWords
	}
	if c.Threshold > 0 {
		threshold = c.Threshold
	}
	return brake, doom, threshold
}
