package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/shared/logger"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/shared/middleware"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/cache"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/client/authclient"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/config"
	handlers "github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/http"
	customMiddleware "github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/middleware"
	metricsMiddleware "github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/middleware"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/rabbit"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/repository"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/service"
)

func main() {
	logrusLogger := logger.Init("tasks")

	cfg, err := config.Load()
	if err != nil {
		logrusLogger.WithError(err).Fatal("failed to load config")
	}

	// Добавляем instance_id в логи
	logrusLogger = logrusLogger.WithField("instance_id", cfg.InstanceID).Logger

	logrusLogger.WithFields(map[string]interface{}{
		"port":           cfg.TasksPort,
		"instance_id":    cfg.InstanceID,
		"auth_addr":      cfg.AuthGRPCAddr,
		"redis_addr":     cfg.Redis.Addr,
		"db_host":        cfg.DB.Host,
		"rabbitmq_url":   cfg.RabbitMQ.URL,
		"rabbitmq_queue": cfg.RabbitMQ.Queue,
	}).Info("starting tasks service")

	// Инициализация репозитория (БД)
	var repo repository.TaskRepository
	if cfg.DB.Driver == "postgres" {
		postgresRepo, err := repository.NewPostgresTaskRepository(cfg.DB.DSN())
		if err != nil {
			logrusLogger.WithError(err).Fatal("failed to connect to database")
		}
		defer postgresRepo.Close()
		repo = postgresRepo
	} else {
		logrusLogger.Fatal("unsupported database driver: " + cfg.DB.Driver)
	}

	// Инициализация клиента Auth (gRPC)
	authClient, err := authclient.NewClient(cfg.AuthGRPCAddr, 2*time.Second, logrusLogger)
	if err != nil {
		logrusLogger.WithError(err).Fatal("failed to create auth client")
	}
	defer authClient.Close()

	// Инициализация клиента Redis (кэш)
	cacheClient := cache.NewClient(
		cfg.Redis.Addr,
		cfg.Redis.Password,
		cfg.Redis.DB,
		cfg.Redis.TTLSeconds,
		cfg.Redis.JitterSeconds,
		logrusLogger,
	)
	defer cacheClient.Close()

	// Проверяем подключение к Redis
	if err := cacheClient.Ping(context.Background()); err != nil {
		logrusLogger.WithError(err).Warn("redis not available, continuing without cache")
	} else {
		logrusLogger.Info("redis connected successfully")
	}

	// Инициализация RabbitMQ продюсера (best effort)
	rabbitProducer, err := rabbit.NewProducer(cfg.RabbitMQ.URL, cfg.RabbitMQ.Queue, logrusLogger)
	if err != nil {
		logrusLogger.WithError(err).Warn("RabbitMQ not available, continuing without events")
	} else {
		defer rabbitProducer.Close()
		logrusLogger.Info("RabbitMQ producer initialized")
	}

	// Инициализация сервиса с кэшем и продюсером
	taskService := service.NewTaskService(repo, cacheClient, rabbitProducer, logrusLogger)

	// Инициализация хендлера
	taskHandler := handlers.NewTaskHandler(taskService, authClient, logrusLogger)

	// Настройка роутера
	mux := http.NewServeMux()

	// Health endpoint (не требует аутентификации)
	mux.HandleFunc("GET /health", taskHandler.HealthHandler)

	// Основные endpoints
	mux.HandleFunc("POST /v1/tasks", taskHandler.CreateTask)
	mux.HandleFunc("GET /v1/tasks", taskHandler.ListTasks)
	mux.HandleFunc("GET /v1/tasks/{id}", taskHandler.GetTask)
	mux.HandleFunc("PATCH /v1/tasks/{id}", taskHandler.UpdateTask)
	mux.HandleFunc("DELETE /v1/tasks/{id}", taskHandler.DeleteTask)
	mux.HandleFunc("GET /v1/tasks/search", taskHandler.SearchTasks)
	mux.Handle("GET /metrics", metricsMiddleware.MetricsHandler())

	// Цепочка middleware
	handler := middleware.RequestIDMiddleware(mux)
	handler = customMiddleware.SecurityHeadersMiddleware(handler)
	handler = customMiddleware.CSRFMiddleware(handler)
	handler = metricsMiddleware.MetricsMiddleware(handler)
	handler = middleware.LoggingMiddleware(handler)

	addr := fmt.Sprintf(":%s", cfg.TasksPort)

	// Graceful shutdown
	server := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logrusLogger.WithField("port", cfg.TasksPort).Info("tasks service started")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logrusLogger.WithError(err).Fatal("server failed")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrusLogger.Info("Shutting down tasks service...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logrusLogger.WithError(err).Fatal("server forced to shutdown")
	}

	logrusLogger.Info("server exited")
}
