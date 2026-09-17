package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/nexusriot/redis-walker/pkg/config"
	"github.com/nexusriot/redis-walker/pkg/controller"
	"github.com/nexusriot/redis-walker/pkg/model"
	"github.com/nexusriot/redis-walker/pkg/view"
)

func main() {
	err := run(os.Args[1:])
	switch {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		// "-h" already printed the usage.
		os.Exit(2)
	default:
		log.WithError(err).Error("redis-walker exited with error")
		os.Exit(1)
	}
}

func run(args []string) error {
	var (
		flags       config.Flags
		showVersion bool
		configPath  string
	)
	flags.Host.Value = "127.0.0.1"
	flags.Port.Value = "6379"

	fs := flag.NewFlagSet("redis-walker", flag.ContinueOnError)
	fs.Var(&flags.Host, "host", "redis host")
	fs.Var(&flags.Port, "port", "redis port")
	fs.Var(&flags.DB, "db", "redis database index")
	fs.Var(&flags.Debug, "debug", "enable debug logging")
	fs.Var(&flags.Username, "username", "redis username (ACL user, optional)")
	fs.Var(&flags.Password, "password", "redis password (optional)")
	fs.Var(&flags.Exclude, "exclude-prefixes",
		"comma-separated list of key prefixes to exclude (e.g. '/pcp:,/metrics:')")
	fs.StringVar(&configPath, "config", config.Path(), "path to the config file (optional)")
	fs.BoolVar(&showVersion, "version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if showVersion {
		fmt.Println("redis-walker", view.Version)
		return nil
	}

	log.SetOutput(os.Stderr)
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})

	cfg, err := config.Load(configPath)
	if err != nil {
		log.WithError(err).Warn("failed to load config file, using flags/defaults only")
		cfg = &config.Config{}
	}

	settings, err := config.Resolve(&flags, cfg)
	if err != nil {
		return err
	}

	if settings.Debug {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	log.WithFields(log.Fields{
		"host":             settings.Host,
		"port":             settings.Port,
		"db":               settings.DB,
		"debug":            settings.Debug,
		"username":         settings.Username,
		"auth_enabled":     settings.Password != "",
		"exclude_prefixes": settings.ExcludePrefixes,
		"config_path":      configPath,
		"version":          view.Version,
	}).Info("Starting redis-walker")

	m, err := model.New(model.Options{
		Host:            settings.Host,
		Port:            settings.Port,
		DB:              settings.DB,
		Username:        settings.Username,
		Password:        settings.Password,
		ExcludePrefixes: settings.ExcludePrefixes,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to redis: %w", err)
	}
	defer m.Close()

	ctrl := controller.NewController(m, settings.Host, settings.Port, settings.DB, settings.Debug)
	return ctrl.Run()
}
