package config

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("a missing config must be tolerated, got %v", err)
	}
	if cfg == nil || cfg.Host != "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

// Regression: an empty file made json.Decode return io.EOF, which was reported
// as a broken config.
func TestLoadEmptyFileIsNotAnError(t *testing.T) {
	cfg, err := Load(writeFile(t, ""))
	if err != nil {
		t.Fatalf("an empty config must be tolerated, got %v", err)
	}
	if cfg.Host != "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	if _, err := Load(writeFile(t, "{oops")); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestLoadFull(t *testing.T) {
	cfg, err := Load(writeFile(t, `{
		"host": "10.0.0.5", "port": "6380", "db": 3, "debug": true,
		"username": "walker", "password": "s3cret",
		"exclude_prefixes": ["/pcp:", "/metrics:"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "10.0.0.5" || cfg.Port != "6380" || *cfg.DB != 3 || !*cfg.Debug {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Username != "walker" || cfg.Password != "s3cret" {
		t.Fatalf("auth not parsed: %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.ExcludePrefixes, []string{"/pcp:", "/metrics:"}) {
		t.Fatalf("exclude = %v", cfg.ExcludePrefixes)
	}
}

func TestPathHonoursEnv(t *testing.T) {
	t.Setenv(EnvConfigPath, "/tmp/custom.json")
	if got := Path(); got != "/tmp/custom.json" {
		t.Fatalf("Path() = %q", got)
	}
	t.Setenv(EnvConfigPath, "  ")
	if got := Path(); got != DefaultConfigPath {
		t.Fatalf("Path() = %q", got)
	}
}

func TestParseExcludeList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"/a:", []string{"/a:"}},
		{" /a: , /b: ,,", []string{"/a:", "/b:"}},
	}
	for _, c := range cases {
		if got := ParseExcludeList(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseExcludeList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Regression: -debug was registered with flag.Var, which demands an argument
// unless the value implements IsBoolFlag. "redis-walker -debug" used to fail.
func TestBoolFlagNeedsNoArgument(t *testing.T) {
	var f Flags
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Var(&f.Debug, "debug", "")
	if err := fs.Parse([]string{"-debug"}); err != nil {
		t.Fatalf("-debug without a value must work, got %v", err)
	}
	if !f.Debug.Value || !f.Debug.Present {
		t.Fatalf("debug flag = %+v", f.Debug)
	}
}

func TestBoolFlagExplicitValue(t *testing.T) {
	var f Flags
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.Var(&f.Debug, "debug", "")
	if err := fs.Parse([]string{"-debug=false"}); err != nil {
		t.Fatal(err)
	}
	if f.Debug.Value || !f.Debug.Present {
		t.Fatalf("debug flag = %+v", f.Debug)
	}
}

func TestResolveDefaults(t *testing.T) {
	s, err := Resolve(&Flags{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Host != "127.0.0.1" || s.Port != "6379" || s.DB != 0 || s.Debug {
		t.Fatalf("settings = %+v", s)
	}
}

func TestResolveConfigUsedWhenFlagsAbsent(t *testing.T) {
	db, debug := 4, true
	cfg := &Config{
		Host: "h", Port: "6380", DB: &db, Debug: &debug,
		Username: "u", Password: "p", ExcludePrefixes: []string{"/x:"},
	}
	s, err := Resolve(&Flags{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Host != "h" || s.Port != "6380" || s.DB != 4 || !s.Debug ||
		s.Username != "u" || s.Password != "p" ||
		!reflect.DeepEqual(s.ExcludePrefixes, []string{"/x:"}) {
		t.Fatalf("settings = %+v", s)
	}
}

func TestResolveFlagsOverrideConfig(t *testing.T) {
	db, debug := 4, true
	cfg := &Config{Host: "h", Port: "6380", DB: &db, Debug: &debug,
		Username: "u", Password: "p", ExcludePrefixes: []string{"/x:"}}

	f := &Flags{}
	f.Host = StringFlag{Value: "1.2.3.4", Present: true}
	f.Port = StringFlag{Value: "7000", Present: true}
	f.DB = StringFlag{Value: "9", Present: true}
	f.Debug = BoolFlag{Value: false, Present: true}
	f.Username = StringFlag{Value: "", Present: true}
	f.Password = StringFlag{Value: "", Present: true}
	f.Exclude = StringFlag{Value: "/a:,/b:", Present: true}

	s, err := Resolve(f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.Host != "1.2.3.4" || s.Port != "7000" || s.DB != 9 || s.Debug {
		t.Fatalf("settings = %+v", s)
	}
	// An explicitly empty -username/-password must disable the config values.
	if s.Username != "" || s.Password != "" {
		t.Fatalf("explicit empty credentials ignored: %+v", s)
	}
	if !reflect.DeepEqual(s.ExcludePrefixes, []string{"/a:", "/b:"}) {
		t.Fatalf("exclude = %v", s.ExcludePrefixes)
	}
}

// Regression: an unparsable -db used to be silently replaced by 0, so the user
// browsed the wrong database.
func TestResolveRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		f    Flags
		cfg  *Config
	}{
		{"db not a number", Flags{DB: StringFlag{Value: "abc", Present: true}}, nil},
		{"negative db", Flags{DB: StringFlag{Value: "-1", Present: true}}, nil},
		{"port not a number", Flags{Port: StringFlag{Value: "redis", Present: true}}, nil},
		{"negative db in config", Flags{}, &Config{DB: intPtr(-2)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Resolve(&c.f, c.cfg); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func intPtr(i int) *int { return &i }
