package main

import (
	"flag"
	"os"
	"strconv"

	log "github.com/sirupsen/logrus"

	"github.com/nexusriot/redis-walker/pkg/config"
	"github.com/nexusriot/redis-walker/pkg/controller"
	"github.com/nexusriot/redis-walker/pkg/model"
)

type stringFlag struct {
	value string
	set   bool
}

func (f *stringFlag) String() string { return f.value }
func (f *stringFlag) Set(s string) error {
	f.value = s
	f.set = true
	return nil
}

type boolFlag struct {
	value bool
	set   bool
}

func (f *boolFlag) String() string { return strconv.FormatBool(f.value) }
func (f *boolFlag) Set(s string) error {
	v, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	f.value = v
	f.set = true
	return nil
}

func main() {
	var (
		hostFlag    = &stringFlag{value: "127.0.0.1"}
		portFlag    = &stringFlag{value: "6379"}
		dbFlag      = &stringFlag{value: "0"}
		debugFlag   = &boolFlag{value: false}
		excludeFlag = &stringFlag{value: ""} // comma-separated prefixes
	)

	flag.Var(hostFlag, "host", "redis host (default: 127.0.0.1)")
	flag.Var(portFlag, "port", "redis port (default: 6379)")
	flag.Var(dbFlag, "db", "redis database index (default: 0)")
	flag.Var(debugFlag, "debug", "enable debug logging (true/false)")
	flag.Var(excludeFlag, "exclude-prefixes",
		"comma-separated list of key prefixes to exclude (e.g. '/pcp:,/metrics:')")
	flag.Parse()

	// Logging setup
	log.SetOutput(os.Stderr)
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	// Load config (optional)
	cfg, err := config.Load(config.DefaultConfigPath)
	if err != nil {
		log.WithError(err).Warn("failed to load config file, using flags/defaults only")
		cfg = &config.Config{}
	}

	// Resolve host: flag wins over config, else default in flag struct.
	host := hostFlag.value
	if !hostFlag.set && cfg.Host != "" {
		host = cfg.Host
	}

	// Resolve port
	port := portFlag.value
	if !portFlag.set && cfg.Port != "" {
		port = cfg.Port
	}

	// Resolve DB index
	var dbIdx int
	if dbFlag.set {
		tmp, err := strconv.Atoi(dbFlag.value)
		if err != nil || tmp < 0 {
			dbIdx = 0
		} else {
			dbIdx = tmp
		}
	} else if cfg.DB != nil && *cfg.DB >= 0 {
		dbIdx = *cfg.DB
	} else {
		dbIdx = 0
	}

	// Resolve debug
	debug := debugFlag.value
	if !debugFlag.set && cfg.Debug != nil {
		debug = *cfg.Debug
	}

	// Resolve exclude prefixes
	var excludePrefixes []string
	if excludeFlag.set {
		excludePrefixes = config.ParseExcludeList(excludeFlag.value)
	} else if len(cfg.ExcludePrefixes) > 0 {
		excludePrefixes = cfg.ExcludePrefixes
	}

	if debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	log.WithFields(log.Fields{
		"host":             host,
		"port":             port,
		"db":               dbIdx,
		"debug":            debug,
		"exclude_prefixes": excludePrefixes,
		"config_path":      config.DefaultConfigPath,
	}).Info("Starting redis-walker")

	m, err := model.NewModel(host, port, dbIdx, excludePrefixes)
	if err != nil {
		log.WithError(err).Error("failed to create Redis model")
		os.Exit(1)
	}

	ctrl := controller.NewController(m, host, port, dbIdx, debug)
	if err := ctrl.Run(); err != nil {
		log.WithError(err).Error("redis-walker exited with error")
		os.Exit(1)
	}
}
