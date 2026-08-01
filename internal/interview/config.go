package interview

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/sean-reid/interviews/internal/fileio"
)

// ConfigFile holds the per-machine settings, beside the session registry.
const ConfigFile = "config.json"

// ContentEnv names the content tree when there is no configured root.
const ContentEnv = "INTERVIEWS_CONTENT"

// FallbackContentRoot is the last resort: a content tree in the working
// directory, which is what a single repo holding both used to mean.
const FallbackContentRoot = "./content"

// Config is what this machine knows that no repository can: chiefly where
// the problems are, since the platform and the content live apart.
type Config struct {
	ContentRoot string `json:"content_root,omitempty"`
}

func configPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ConfigFile), nil
}

// LoadConfig reads the machine config. A missing or unreadable file is an
// empty config, not an error: every setting has a fallback, and a broken
// config must not stop an interview.
func LoadConfig() *Config {
	p, err := configPath()
	if err != nil {
		return &Config{}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return &Config{}
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return &Config{}
	}
	return &c
}

// SaveConfig writes the machine config.
func SaveConfig(c *Config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fileio.WriteAtomic(p, raw, 0o600)
}

// ContentSource says where a content root came from, so a command can
// report it and a wrong one is traceable to what set it.
type ContentSource string

// Where a resolved content root came from, in resolution order.
const (
	FromFlag     ContentSource = "--content"
	FromConfig   ContentSource = "config"
	FromEnv      ContentSource = ContentSource(ContentEnv)
	FromFallback ContentSource = "default"
)

// ContentRoot resolves where the problems are: the config, then the
// environment, then a content directory here. The flag beats all of it, and
// the caller applies that by treating a set flag as the answer.
func ContentRoot() (string, ContentSource) {
	if root := LoadConfig().ContentRoot; root != "" {
		return root, FromConfig
	}
	if root := os.Getenv(ContentEnv); root != "" {
		return root, FromEnv
	}
	return FallbackContentRoot, FromFallback
}
