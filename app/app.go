package app

import (
	"bytes"
	"caching-proxy-server/internal/cache"
	"caching-proxy-server/internal/server"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"
)

const CheckOriginTimeout = 5
const DefaultHTTPTimeout = 5

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

	srv, err := a.newServer()
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Timeout: DefaultHTTPTimeout * time.Second,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", makeHandler(httpClient, a.responseCache, *a.url))
	srv.Handler = mux

	return srv, nil
}

func (a *App) newServer() (*http.Server, error) {
	ln, err := net.Listen("tcp", ":"+a.port)
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		ErrorLog:          slog.NewLogLogger(a.logHandler, a.logLevel.Level()),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(http.ErrServerClosed, serveErr) {
			slog.Error("server serve failed", slog.String("err", serveErr.Error()))
		}
	}()

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

func makeHandler(client *http.Client, cache *cache.Cache, baseUrl url.URL) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Получен запрос",
			slog.Group("Request",
				slog.String("method", r.Method),
				slog.String("url", r.URL.String()),
				slog.String("remote", r.RemoteAddr),
				slog.String("ua", r.UserAgent()),
			),
		)

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			slog.Error("Ошибка при чтении body запроса от клиента", slog.String("err", err.Error()))
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		hashedRequest := server.Hash(r.URL.String(), r.Method, r.Header, bodyBytes)
		slog.Debug("Хэш запись для запроса", slog.String("hash.key", hashedRequest))
		resp, exist := cache.Get(hashedRequest)

		if exist {
			slog.Info("Ответ взят из кэша")
		}

		if !exist {
			slog.Info("Ответ не найден в кэше")
			targetURL := *r.URL
			targetURL.Scheme = baseUrl.Scheme
			targetURL.Host = baseUrl.Host

			proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), bytes.NewReader(bodyBytes))
			if err != nil {
				slog.Error("Ошибка при создании прокси-запроса", slog.String("err", err.Error()))
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}

			slog.Debug("Построили прокси запрос")
			// Copy headers from original request
			proxyReq.Header = r.Header.Clone()

			httpResp, err := client.Do(proxyReq)
			if err != nil {
				slog.Error("Ошибка при выполнении запроса", slog.String("err", err.Error()))
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer httpResp.Body.Close()
			slog.Debug("Отправили прокси запрос к origin")

			resp, err = cache.Set(hashedRequest, httpResp)
			if err != nil {
				slog.Error("Ошибка при записи значения в кэш", slog.String("err", err.Error()))
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			slog.Debug("Сохранили ответ в кэш")
		}

		// добавляем хэддеры
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}

		// пишем http статус для ответа
		w.WriteHeader(resp.StatusCode)

		_, err = w.Write(resp.Body)
		if err != nil {
			slog.Error("Cannot write response",
				"response", resp,
				"err", err.Error())
			return
		}
	}
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

	slog.Info("Успешный пинг origin",
		slog.Group("server.ping",
			slog.Duration("delay", time.Since(beforeRequest)),
			slog.Int("status", resp.StatusCode),
		),
	)
	return err
}
