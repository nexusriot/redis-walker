package config

import (
	"encoding/json"
	"os"
	"strings"
)

// Config is loaded from /etc/redis-walker/config.json
// CLI flags override any values defined here.
type Config struct {
	Host            string   `json:"host"`
	Port            string   `json:"port"`
	DB              *int     `json:"db"`
	Debug           *bool    `json:"debug"`
	Username        string   `json:"username"`         // optional Redis ACL username
	Password        string   `json:"password"`         // optional Redis password
	ExcludePrefixes []string `json:"exclude_prefixes"` // key prefixes to hide
}

const DefaultConfigPath = "/etc/redis-walker/config.json"

// Load reads config from the given path. If the file does not exist,
// it returns an empty config and no error.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath
	}

	f, err := os.Open(path)
	if os.IsNotExist(err) {
		// Config is optional
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ParseExcludeList parses comma-separated prefixes.
func ParseExcludeList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
