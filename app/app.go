package app

import (
	"caching-proxy-server/internal/cache"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

const CheckOriginTimeout = 5

type App struct {
	// Поля, получаемые с конструктора
	origin, port  string
	logLevel      *slog.LevelVar
	responseCache *cache.Cache
	// Личные поля
	logHandler *slog.TextHandler
	logger     *slog.Logger
	url        *url.URL
}

func NewApp(origin, port string, logLevel *slog.LevelVar, cache *cache.Cache) *App {
	return &App{
		origin:        origin,
		port:          port,
		logLevel:      logLevel,
		responseCache: cache,
	}
}

func (a *App) Run() (*http.Server, error) {
	// Инициализация логгера
	logger := a.newLogger()
	a.logger = logger
	slog.SetDefault(logger)

	// Парсинг origin в URL
	u, err := parseOrigin(a.origin)
	if err != nil {
		return nil, err
	}
	a.url = &u

	// Пинг origin
	err = a.pingOrigin()
	if err != nil {
		return nil, err
	}

	// TODO перенести это в функцию a.NewServer
	ln, err := net.Listen("tcp", ":"+a.port)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		ErrorLog: slog.NewLogLogger(a.logHandler, a.logLevel.Level()),
	}

	// TODO нужна ли тут горутина?
	go func() {
		if err = srv.Serve(ln); err != nil && !errors.Is(http.ErrServerClosed, err) {
			log.Fatal(err)
		}
	}()
	// TODO перенести это в функцию a.NewServer

	// TODO Init cache struct
	// TODO make handler
	return srv, nil
}

func (a *App) newLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: a.logLevel}
	a.logHandler = slog.NewTextHandler(os.Stdout, opts)

	slogger := slog.New(a.logHandler)

	slogger = slogger.With(
		slog.Group("server",
			slog.String("origin", a.origin),
			slog.String("port", a.port),
		),
	)

	return slogger
}

func parseOrigin(rawURL string) (url.URL, error) {
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return url.URL{}, fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme == "" || u.Host == "" {
		return url.URL{}, fmt.Errorf("missing scheme or host in %q", rawURL)
	} else if u.Scheme != "http" && u.Scheme != "https" {
		return url.URL{}, fmt.Errorf("unsupported scheme %q, only http/https allowed", u.Scheme)
	}

	slog.Debug("Успешный парсинг origin",
		slog.Group("origin",
			slog.String("scheme", u.Scheme),
			slog.String("host", u.Host),
			slog.String("path", u.Path),
		))
	return *u, err
}

func (a *App) pingOrigin() error {
	// Проверка доступности origin
	client := http.Client{Timeout: CheckOriginTimeout * time.Second}
	beforeRequest := time.Now()
	resp, err := client.Head(a.url.String())
	if err != nil {
		return err
	}
	defer func(Body io.ReadCloser) {
		dErr := Body.Close()
		if dErr != nil {
			err = errors.Join(dErr, err)
		}
	}(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("origin %q responded with status %s", a.url.String(), resp.Status)
	}

	slog.Info("Успешный пинг Origin",
		slog.Group("origin.ping",
			slog.Duration("Delay", time.Since(beforeRequest)),
			slog.Int("Status", resp.StatusCode),
		),
	)
	return err
}
