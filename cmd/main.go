package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const HTTPClientTimeout = 5

// Check origin url - validate RFC 3986 standard and ping it.
func checkOrigin(rawURL string) error {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return err
	}

	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("check your scheme or host - one of these is empty")
	} else if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid schema, only http(-s) is allowed")
	}

	// Проверка лишь на доступность origin
	client := http.Client{Timeout: HTTPClientTimeout * time.Second}
	beforeRequest := time.Now()
	resp, err := client.Head(u.String())
	if err != nil {
		return err
	}
	fmt.Println("Success ping", u.String(), "delay to origin - ", time.Since(beforeRequest))

	err = resp.Body.Close()
	return err
}

func cliInit(port *uint, origin *string, logLevel *string) error {
	flag.UintVar(port, "port", 0, "specify port that server will listening")
	flag.StringVar(origin, "origin", "", "origin base url")
	flag.StringVar(logLevel, "logLevel", "DEBUG", "level for logger")
	flag.Parse()
	var err error

	if *port == 0 {
		err = fmt.Errorf("error! - port value is incorrect")
	} else if len(*origin) == 0 {
		err = fmt.Errorf("error! - origin value is incorrect")
	} else if len(*logLevel) == 0 {
		err = fmt.Errorf("error! - log level cannot be empty")
	}

	return err
}

func requestHandler(w http.ResponseWriter, req *http.Request) {
	slog.Info("Получен запрос",
		slog.Group("Request",
			slog.String("method", req.Method),
			slog.String("url", req.URL.String()),
			slog.String("remote", req.RemoteAddr),
			slog.String("ua", req.UserAgent()),
		),
	)

	resp := "Hello, world!\n"
	_, err := io.WriteString(w, resp)
	if err != nil {
		slog.Warn("Cannot write response",
			"response", resp,
			"err", err.Error())
		return
	}
}

func parseAndSetLogLevel(levelString string, logLevel *slog.LevelVar) error {
	var err error
	switch strings.ToUpper(levelString) {
	case "DEBUG":
		logLevel.Set(slog.LevelDebug)
	case "INFO":
		logLevel.Set(slog.LevelInfo)
	case "WARN":
		logLevel.Set(slog.LevelWarn)
	case "ERROR":
		logLevel.Set(slog.LevelError)
	default:
		err = fmt.Errorf("unknown log level")
	}

	return err
}

func main() {
	var port uint
	var origin string
	var err error
	var logLevelString string

	err = cliInit(&port, &origin, &logLevelString)
	if err != nil {
		log.Fatal(err.Error())
	}

	logLevel := &slog.LevelVar{}
	opts := &slog.HandlerOptions{Level: logLevel}
	loggerHandler := slog.NewTextHandler(os.Stdout, opts)

	slogger := slog.New(loggerHandler)
	slogger = slogger.With(
		slog.Group("server",
			slog.String("origin", origin),
			slog.Uint64("port", uint64(port)),
		),
	)
	slog.SetDefault(slogger)

	err = parseAndSetLogLevel(logLevelString, logLevel)
	if err != nil {
		log.Fatal(err.Error())
	}

	err = checkOrigin(origin)
	if err != nil {
		log.Fatal(err.Error())
	}

	portString := strconv.Itoa(int(port))

	ln, err := net.Listen("tcp", ":"+portString)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		ErrorLog: slog.NewLogLogger(loggerHandler, logLevel.Level()),
	}
	http.HandleFunc("/", requestHandler)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err = srv.Serve(ln); err != nil && !errors.Is(http.ErrServerClosed, err) {
			log.Fatal(err)
		}
	}()

	_ = <-c
	slog.Debug("Received shutdown signal")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = srv.Shutdown(ctx); err != nil {
		slog.Error("Shutdown timeout exceed",
			slog.String("err", err.Error()),
		)
	}
	slog.Debug("Graceful shutdown")
}
