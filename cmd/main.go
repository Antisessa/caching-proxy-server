package main

import (
	"caching-proxy-server/app"
	"caching-proxy-server/internal/cache"
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const DefaultCacheSize = 5

func main() {
	var port uint
	var origin string
	var err error
	var logLevelString string
	var cacheSize uint

	err = cliInit(&port, &origin, &logLevelString, &cacheSize)
	if err != nil {
		log.Fatal(err.Error())
	}

	logLevel, err := parseLogLevel(logLevelString)
	if err != nil {
		log.Fatal(err.Error())
	}
	portString := strconv.Itoa(int(port))
	responsesCache := cache.Init(int(cacheSize))

	newApp := app.NewApp(origin, portString, logLevel, responsesCache)
	srv, err := newApp.Run()
	if err != nil {
		log.Fatal(err.Error())
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	_ = <-c
	slog.Debug("Received shutdown signal")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = srv.Shutdown(ctx); err != nil {
		slog.Error("Shutdown timeout exceed",
			slog.String("err", err.Error()),
		)
	}
	slog.Debug("Successful graceful shutdown")
}

func parseLogLevel(levelString string) (*slog.LevelVar, error) {
	var err error
	var res slog.LevelVar
	switch strings.ToUpper(levelString) {
	case "DEBUG":
		res.Set(slog.LevelDebug)
	case "INFO":
		res.Set(slog.LevelInfo)
	case "WARN":
		res.Set(slog.LevelWarn)
	case "ERROR":
		res.Set(slog.LevelError)
	default:
		err = fmt.Errorf("unknown log level")
	}

	return &res, err
}

func cliInit(port *uint, origin *string, logLevel *string, cacheSize *uint) error {
	flag.UintVar(port, "port", 0, "specify port that server will listening")
	flag.StringVar(origin, "origin", "", "origin base url")
	flag.StringVar(logLevel, "logLevel", "DEBUG", "level for logger")
	flag.UintVar(cacheSize, "cacheSize", DefaultCacheSize, "size of cache for responses")
	flag.Parse()

	if *port == 0 {
		return fmt.Errorf("error! - port value is incorrect")
	} else if len(*origin) == 0 {
		return fmt.Errorf("error! - origin value is incorrect")
	} else if len(*logLevel) == 0 {
		return fmt.Errorf("error! - log level cannot be empty")
	}
	return nil
}
