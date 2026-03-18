package rabbit

import (
	"context"
	"encoding/json"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

type TaskEvent struct {
	Event     string `json:"event"`
	TaskID    string `json:"task_id"`
	Title     string `json:"title"`
	Timestamp string `json:"ts"`
	RequestID string `json:"request_id,omitempty"`
}

type Consumer struct {
	conn          *amqp.Connection
	channel       *amqp.Channel
	queue         string
	prefetchCount int
	logger        *logrus.Logger
}

func NewConsumer(rabbitURL, queueName string, prefetchCount int, logger *logrus.Logger) (*Consumer, error) {
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	// Настраиваем prefetch
	err = ch.Qos(
		prefetchCount, // prefetch count
		0,             // prefetch size
		false,         // global
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to set QoS: %w", err)
	}

	// Объявляем очередь (должна совпадать с producer)
	_, err = ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		ch.Close()
		conn.Close()
		return nil, fmt.Errorf("failed to declare queue: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"queue":    queueName,
		"prefetch": prefetchCount,
	}).Info("RabbitMQ consumer connected")

	return &Consumer{
		conn:          conn,
		channel:       ch,
		queue:         queueName,
		prefetchCount: prefetchCount,
		logger:        logger,
	}, nil
}

func (c *Consumer) StartConsuming(ctx context.Context) error {
	msgs, err := c.channel.Consume(
		c.queue, // queue
		"",      // consumer
		false,   // auto-ack (мы будем ack сами)
		false,   // exclusive
		false,   // no-local
		false,   // no-wait
		nil,     // args
	)
	if err != nil {
		return fmt.Errorf("failed to register consumer: %w", err)
	}

	c.logger.Info("Worker started, waiting for messages...")

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("Stopping consumer...")
			return nil
		case msg := <-msgs:
			c.processMessage(msg)
		}
	}
}

func (c *Consumer) processMessage(msg amqp.Delivery) {
	logEntry := c.logger.WithField("delivery_tag", msg.DeliveryTag)

	var event TaskEvent
	if err := json.Unmarshal(msg.Body, &event); err != nil {
		logEntry.WithError(err).Error("Failed to unmarshal message")
		// Nack without requeue (кладём в dead letter или отбрасываем)
		msg.Nack(false, false)
		return
	}

	logEntry = logEntry.WithFields(logrus.Fields{
		"event":      event.Event,
		"task_id":    event.TaskID,
		"title":      event.Title,
		"request_id": event.RequestID,
	})

	logEntry.Info("✅ Received task event")

	// Здесь может быть реальная обработка
	// Например, отправка email, обновление поискового индекса и т.д.

	// Подтверждаем успешную обработку
	if err := msg.Ack(false); err != nil {
		logEntry.WithError(err).Error("Failed to ack message")
	}
}

func (c *Consumer) Close() error {
	var err error
	if c.channel != nil {
		if e := c.channel.Close(); e != nil {
			err = e
		}
	}
	if c.conn != nil {
		if e := c.conn.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}
