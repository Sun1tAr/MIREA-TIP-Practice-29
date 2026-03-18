package config

import (
	"os"
	"strconv"
)

type Config struct {
	RabbitMQURL   string
	QueueName     string
	PrefetchCount int
	LogLevel      string
}

func Load() *Config {
	return &Config{
		RabbitMQURL:   getEnv("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		QueueName:     getEnv("RABBITMQ_QUEUE", "task_events"),
		PrefetchCount: getEnvAsInt("RABBITMQ_PREFETCH", 1),
		LogLevel:      getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
