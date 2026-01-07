package main

import (
	"bytes"
	"caching-proxy-server/internal/cache"
	"caching-proxy-server/internal/server"
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

const DefaultHTTPTimeout = 5
const DefaultCacheSize = 5

func switchBodyToNopCloser(r *http.Request) error {
	bodyBytes, err := io.ReadAll(r.Body) // читаем из оригинального body и закрываем дескриптор
	// r.Body.Close() Не закрываем!, оставляем дескриптор с io.EOF,
	// после хэндлера body закроется сервером автоматически

	if err != nil {
		return err
	}

	r.Body = io.NopCloser(bytes.NewReader(bodyBytes)) // подменяем body на NopCloser
	return nil
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

		err := switchBodyToNopCloser(r)
		if err != nil {
			slog.Error("Ошибка при подмене body запроса", slog.String("err", err.Error()))
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		hashedRequest, err := server.Hash(*r)
		if err != nil {
			slog.Error("Ошибка при вычислении хэша запроса", slog.String("err", err.Error()))
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		resp, exist := cache.Get(hashedRequest)

		if exist {
			slog.Info("Ответ взят из кэша")
		}

		if !exist {
			targetURL := *r.URL
			targetURL.Scheme = baseUrl.Scheme
			targetURL.Host = baseUrl.Host

			bodyBytes, err := io.ReadAll(r.Body)
			proxyReq, err := http.NewRequest(r.Method, targetURL.String(), bytes.NewReader(bodyBytes))
			if err != nil {
				slog.Error("Ошибка при создании прокси-запроса", slog.String("err", err.Error()))
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			// Copy headers from original request
			proxyReq.Header = r.Header.Clone()

			httpResp, err := client.Do(proxyReq)
			if err != nil {
				slog.Error("Ошибка при выполнении запроса", slog.String("err", err.Error()))
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer httpResp.Body.Close()

			resp, err = cache.Set(hashedRequest, httpResp)
			if err != nil {
				slog.Error("Ошибка при записи значения в кэш", slog.String("err", err.Error()))
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
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

	ln, err := net.Listen("tcp", ":"+portString)
	if err != nil {
		log.Fatal(err)
	}

	srv := &http.Server{
		ErrorLog: slog.NewLogLogger(loggerHandler, logLevel.Level()),
	}

	responsesCache := cache.Init(int(cacheSize))
	httpClient := &http.Client{
		Timeout: DefaultHTTPTimeout * time.Second,
	}

	handler := makeHandler(httpClient, responsesCache, parsedUrl)
	http.HandleFunc("/", handler)

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
