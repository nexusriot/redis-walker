package main

import (
	"flag"
	"os"
	"strconv"

	log "github.com/sirupsen/logrus"

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
		hostFlag  = &stringFlag{value: "127.0.0.1"}
		portFlag  = &stringFlag{value: "6379"}
		dbFlag    = &stringFlag{value: "0"}
		debugFlag = &boolFlag{value: false}
	)

	flag.Var(hostFlag, "host", "redis host (default: 127.0.0.1)")
	flag.Var(portFlag, "port", "redis port (default: 6379)")
	flag.Var(dbFlag, "db", "redis database index (default: 0)")
	flag.Var(debugFlag, "debug", "enable debug logging (true/false)")
	flag.Parse()

	host := hostFlag.value
	port := portFlag.value
	dbIdx, err := strconv.Atoi(dbFlag.value)
	if err != nil || dbIdx < 0 {
		dbIdx = 0
	}

	log.SetOutput(os.Stderr)
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
	if debugFlag.value {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	log.WithFields(log.Fields{
		"host":  host,
		"port":  port,
		"db":    dbIdx,
		"debug": debugFlag.value,
	}).Info("Starting redis-walker")

	m, err := model.NewModel(host, port, dbIdx)
	if err != nil {
		log.WithError(err).Error("failed to create Redis model")
		os.Exit(1)
	}

	ctrl := controller.NewController(m, host, port, dbIdx, debugFlag.value)
	if err := ctrl.Run(); err != nil {
		log.WithError(err).Error("redis-walker exited with error")
		os.Exit(1)
	}
}
