package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/shared/logger"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/worker/internal/config"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/worker/internal/rabbit"
)

func main() {
	logrusLogger := logger.Init("worker")

	cfg := config.Load()

	logrusLogger.WithFields(map[string]interface{}{
		"rabbitmq_url": cfg.RabbitMQURL,
		"queue":        cfg.QueueName,
		"prefetch":     cfg.PrefetchCount,
	}).Info("starting worker service")

	// Подключаемся к RabbitMQ
	consumer, err := rabbit.NewConsumer(cfg.RabbitMQURL, cfg.QueueName, cfg.PrefetchCount, logrusLogger)
	if err != nil {
		logrusLogger.WithError(err).Fatal("failed to create consumer")
	}
	defer consumer.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Запускаем consumer в горутине
	go func() {
		if err := consumer.StartConsuming(ctx); err != nil {
			logrusLogger.WithError(err).Error("consumer error")
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logrusLogger.Info("Shutting down worker...")

	cancel()
	time.Sleep(1 * time.Second) // Даём время завершить обработку
	logrusLogger.Info("worker exited")
}
