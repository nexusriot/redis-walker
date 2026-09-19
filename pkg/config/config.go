package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// DefaultConfigPath is read at start-up when it exists.
const DefaultConfigPath = "/etc/redis-walker/config.json"

// EnvConfigPath overrides DefaultConfigPath.
const EnvConfigPath = "REDIS_WALKER_CONFIG"

// Config mirrors /etc/redis-walker/config.json. CLI flags override every value
// defined here; pointer fields distinguish "absent" from "zero".
type Config struct {
	Host            string   `json:"host"`
	Port            string   `json:"port"`
	DB              *int     `json:"db"`
	Debug           *bool    `json:"debug"`
	Username        string   `json:"username"`         // optional Redis ACL username
	Password        string   `json:"password"`         // optional Redis password
	ExcludePrefixes []string `json:"exclude_prefixes"` // key prefixes to hide
}

// Load reads the config from path. A missing or empty file is not an error:
// the config is optional and an empty Config is returned.
func Load(path string) (*Config, error) {
	if path == "" {
		path = Path()
	}

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty file behaves like a missing one.
			return &Config{}, nil
		}
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// Path returns the config path, honouring REDIS_WALKER_CONFIG.
func Path() string {
	if p := strings.TrimSpace(os.Getenv(EnvConfigPath)); p != "" {
		return p
	}
	return DefaultConfigPath
}

// ParseExcludeList parses a comma-separated prefix list.
func ParseExcludeList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// StringFlag is a string flag that remembers whether it was set on the command
// line, so that "was not given" can be told apart from "was given the default".
type StringFlag struct {
	Value string
	// Present reports whether the flag appeared on the command line.
	Present bool
}

func (f *StringFlag) String() string {
	if f == nil {
		return ""
	}
	return f.Value
}

// Set implements flag.Value.
func (f *StringFlag) Set(s string) error {
	f.Value = s
	f.Present = true
	return nil
}

// BoolFlag is the bool equivalent of StringFlag.
type BoolFlag struct {
	Value bool
	// Present reports whether the flag appeared on the command line.
	Present bool
}

func (f *BoolFlag) String() string {
	if f == nil {
		return "false"
	}
	return strconv.FormatBool(f.Value)
}

// Set implements flag.Value.
func (f *BoolFlag) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	f.Value = v
	f.Present = true
	return nil
}

// IsBoolFlag makes "-debug" work without an explicit "=true"; without it the
// flag package demands an argument for every flag.Var.
func (f *BoolFlag) IsBoolFlag() bool { return true }

// Flags holds the parsed command line.
type Flags struct {
	Host     StringFlag
	Port     StringFlag
	DB       StringFlag
	Debug    BoolFlag
	Username StringFlag
	Password StringFlag
	Exclude  StringFlag
}

// Settings is the effective configuration after merging flags and file.
type Settings struct {
	Host            string
	Port            string
	DB              int
	Debug           bool
	Username        string
	Password        string
	ExcludePrefixes []string
}

// Resolve merges command-line flags (highest priority), the config file and the
// built-in defaults.
func Resolve(f *Flags, cfg *Config) (*Settings, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	s := &Settings{
		Host:     "127.0.0.1",
		Port:     "6379",
		Username: cfg.Username,
		Password: cfg.Password,
	}

	if f.Host.Present {
		s.Host = f.Host.Value
	} else if cfg.Host != "" {
		s.Host = cfg.Host
	}

	if f.Port.Present {
		s.Port = f.Port.Value
	} else if cfg.Port != "" {
		s.Port = cfg.Port
	}
	if _, err := strconv.Atoi(s.Port); err != nil {
		return nil, fmt.Errorf("invalid port %q", s.Port)
	}

	switch {
	case f.DB.Present:
		db, err := strconv.Atoi(strings.TrimSpace(f.DB.Value))
		if err != nil || db < 0 {
			return nil, fmt.Errorf("invalid db index %q", f.DB.Value)
		}
		s.DB = db
	case cfg.DB != nil:
		if *cfg.DB < 0 {
			return nil, fmt.Errorf("invalid db index %d in config", *cfg.DB)
		}
		s.DB = *cfg.DB
	}

	if f.Debug.Present {
		s.Debug = f.Debug.Value
	} else if cfg.Debug != nil {
		s.Debug = *cfg.Debug
	}

	if f.Username.Present {
		s.Username = f.Username.Value
	}
	if f.Password.Present {
		s.Password = f.Password.Value
	}

	if f.Exclude.Present {
		s.ExcludePrefixes = ParseExcludeList(f.Exclude.Value)
	} else {
		s.ExcludePrefixes = append([]string(nil), cfg.ExcludePrefixes...)
	}

	return s, nil
}
