package rabbit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"
)

type Producer struct {
	conn    *amqp091.Connection
	channel *amqp091.Channel
	queue   string
	logger  *logrus.Logger
}

type TaskEvent struct {
	Event     string    `json:"event"`
	TaskID    string    `json:"task_id"`
	Title     string    `json:"title"`
	Timestamp time.Time `json:"ts"`
	RequestID string    `json:"request_id,omitempty"`
}

func NewProducer(rabbitURL, queueName string, logger *logrus.Logger) (*Producer, error) {
	conn, err := amqp091.Dial(rabbitURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to open channel: %w", err)
	}

	// Объявляем очередь (durable)
	_, err = ch.QueueDeclare(
		queueName, // name
		true,      // durable (сохраняется при рестарте)
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
		"queue": queueName,
		"url":   rabbitURL,
	}).Info("RabbitMQ producer connected")

	return &Producer{
		conn:    conn,
		channel: ch,
		queue:   queueName,
		logger:  logger,
	}, nil
}

func (p *Producer) PublishTaskCreated(ctx context.Context, taskID, title, requestID string) error {
	event := TaskEvent{
		Event:     "task.created",
		TaskID:    taskID,
		Title:     title,
		Timestamp: time.Now().UTC(),
		RequestID: requestID,
	}

	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	err = p.channel.PublishWithContext(ctx,
		"",      // exchange
		p.queue, // routing key (queue name)
		true,    // mandatory
		false,   // immediate
		amqp091.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp091.Persistent, // сообщения сохраняются на диск
			Timestamp:    time.Now(),
			Body:         body,
		})
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	p.logger.WithFields(logrus.Fields{
		"task_id":    taskID,
		"queue":      p.queue,
		"request_id": requestID,
	}).Info("task.created event published")

	return nil
}

func (p *Producer) Close() error {
	var err error
	if p.channel != nil {
		if e := p.channel.Close(); e != nil {
			err = e
		}
	}
	if p.conn != nil {
		if e := p.conn.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}
