# Практическое занятие №13 (29). Подключение к RabbitMQ. Отправка и получение сообщений

## Выполнил: Туев Д. ЭФМО-01-25

## Содержание

1. [Описание проекта](#описание-проекта)
2. [Архитектура решения](#архитектура-решения)
3. [Поднятие RabbitMQ](#поднятие-rabbitmq)
4. [Формат сообщения](#формат-сообщения)
5. [Producer: публикация события из tasks](#producer-публикация-события-из-tasks)
6. [Consumer: worker для обработки сообщений](#consumer-worker-для-обработки-сообщений)
7. [Запуск системы](#запуск-системы)
8. [Скриншоты выполнения](#скриншоты-выполнения)
9. [Выводы](#выводы)
10. [Контрольные вопросы](#контрольные-вопросы)

---

## Описание проекта

В рамках практического занятия №13 на базе сервиса **Tasks** реализована интеграция с брокером сообщений **RabbitMQ**. При создании задачи через REST API сервис публикует событие в очередь, а отдельный сервис **Worker** потребляет эти сообщения и логирует их.

**Цель работы:** Научиться поднимать RabbitMQ, публиковать сообщения в очередь и обрабатывать их потребителем с подтверждением (ack), понимая основы надёжности доставки.

### Реализованный сценарий

1. Клиент вызывает `POST /v1/tasks`
2. Сервис **tasks** создаёт задачу в БД
3. **tasks** публикует сообщение в очередь `task_events` RabbitMQ
4. Сервис **worker** читает сообщения из очереди и логирует: "получено событие task.created id=..."

---

## Архитектура решения

### Компоненты системы

| Компонент | Роль | Порт |
|-----------|------|------|
| **RabbitMQ** | Брокер сообщений | 5672 (AMQP), 15672 (Management) |
| **tasks** | Producer, публикует события при создании задач | 8082 |
| **worker** | Consumer, обрабатывает события из очереди | - |
| **PostgreSQL** | Хранилище данных задач | 5432 |
| **auth** | Аутентификация и cookies | 8081, 50051 |
| **redis** | Кэширование задач | 6379 |

### Схема взаимодействия

```mermaid
graph LR
    Client[Клиент] -->|POST /v1/tasks| Tasks[Tasks Service]
    Tasks -->|1. Создание| DB[(PostgreSQL)]
    Tasks -->|2. Публикация события| RMQ[RabbitMQ]
    RMQ -->|3. Потребление| Worker[Worker Service]
    Worker -->|4. Логирование| Logs[Логи]
```

### Структура проекта (добавленные/изменённые файлы)

```
tech-ip-sem2/
├── deploy/
│   ├── docker-compose.yml              # добавлен rabbitmq и worker
│   └── rabbit/
│       └── docker-compose.yml           # отдельный compose для rabbit
├── services/
│   ├── tasks/
│   │   ├── internal/
│   │   │   ├── rabbit/
│   │   │   │   └── producer.go          # новый: публикация в RabbitMQ
│   │   │   ├── config/
│   │   │   │   └── config.go            # обновлён: добавлена конфигурация RabbitMQ
│   │   │   ├── service/
│   │   │   │   └── tasks.go             # обновлён: публикация события после создания
│   │   │   └── ...
│   │   └── cmd/
│   │       └── tasks/
│   │           └── main.go              # обновлён: инициализация продюсера
│   └── worker/                           # новый сервис
│       ├── cmd/
│       │   └── worker/
│       │       └── main.go
│       ├── internal/
│       │   ├── config/
│       │   │   └── config.go
│       │   └── rabbit/
│       │       └── consumer.go
│       ├── .dockerignore
│       ├── Dockerfile
│       ├── go.mod
│       └── go.sum
└── shared/
    └── ...                               # без изменений
```

---

## Поднятие RabbitMQ

### Конфигурация Docker Compose

Основной `docker-compose.yml` был дополнен сервисами `rabbitmq` и `worker`:

```yaml
rabbitmq:
  image: rabbitmq:3.13-management-alpine
  container_name: rabbitmq
  hostname: rabbitmq
  ports:
    - "5672:5672"      # AMQP protocol
    - "15672:15672"    # Management UI
  environment:
    RABBITMQ_DEFAULT_USER: guest
    RABBITMQ_DEFAULT_PASS: guest
  volumes:
    - rabbitmq_data:/var/lib/rabbitmq
  healthcheck:
    test: ["CMD", "rabbitmq-diagnostics", "ping"]
    interval: 10s
    timeout: 5s
    retries: 5
  networks:
    - app-network

worker:
  build:
    context: ../services/worker
    dockerfile: Dockerfile
  container_name: worker-service
  environment:
    RABBITMQ_URL: amqp://guest:guest@rabbitmq:5672/
    RABBITMQ_QUEUE: task_events
    RABBITMQ_PREFETCH: 1
    LOG_LEVEL: debug
  depends_on:
    rabbitmq:
      condition: service_healthy
  networks:
    - app-network
  restart: unless-stopped
```

### Запуск RabbitMQ

```bash
cd deploy
docker-compose up -d rabbitmq
# или полностью весь стек:
docker-compose up -d
```

### Параметры подключения

| Параметр | Значение |
|----------|----------|
| AMQP URL | `amqp://guest:guest@localhost:5672/` |
| Management UI | http://localhost:15672 |
| Логин/пароль | guest/guest |
| Имя очереди | `task_events` |

---

## Формат сообщения

Для события выбран формат JSON, что обеспечивает гибкость и удобство отладки.

### Структура события `task.created`

```json
{
  "event": "task.created",
  "task_id": "t_001",
  "title": "Название задачи",
  "ts": "2026-03-18T12:00:00Z",
  "request_id": "pz29-550e8400-e29b-41d4-a716-446655440000"
}
```

### Описание полей

| Поле | Тип | Описание |
|------|-----|----------|
| `event` | string | Тип события (task.created) |
| `task_id` | string | Идентификатор созданной задачи |
| `title` | string | Название задачи (для отладки) |
| `ts` | string | Временная метка в UTC |
| `request_id` | string | ID запроса для трассировки |

### Почему выбран такой формат

- **JSON** — человекочитаемый, легко парсится, поддерживается всеми языками
- **event** — позволяет в будущем добавлять другие типы событий (task.updated, task.deleted)
- **task_id** — минимально достаточная информация для идентификации
- **title** — добавлен для удобства отладки (в логах сразу видно, о какой задаче речь)
- **ts** — для хронологии и возможных задержек
- **request_id** — сквозная трассировка запроса через все сервисы

---

## Producer: публикация события из tasks

### Где публикуется сообщение

Сообщение публикуется **после успешного создания задачи в БД**, в методе `Create` сервиса задач. Это гарантирует, что событие не будет отправлено, если задача не создана.

### Best effort подход

В учебной реализации выбран подход **"best effort"**:
- Если RabbitMQ недоступен, задача всё равно создаётся
- Ошибка публикации логируется, но не влияет на ответ клиенту
- Это обеспечивает отказоустойчивость основного сервиса

### Код продюсера

**`internal/rabbit/producer.go`** — основной код для публикации:

```go
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
        "",          // exchange
        p.queue,     // routing key (queue name)
        true,        // mandatory
        false,       // immediate
        amqp091.Publishing{
            ContentType:  "application/json",
            DeliveryMode: amqp091.Persistent, // сообщения сохраняются на диск
            Timestamp:    time.Now(),
            Body:         body,
        })
    
    return err
}
```

### Интеграция в сервис задач

В методе `Create` сервиса `tasks.go` после сохранения в БД:

```go
// Публикуем событие в RabbitMQ (best effort)
if s.producer != nil {
    requestID := middleware.GetRequestID(ctx)
    
    go func() {
        pubCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        defer cancel()
        
        if err := s.producer.PublishTaskCreated(pubCtx, task.ID, task.Title, requestID); err != nil {
            s.log.WithError(err).WithFields(logrus.Fields{
                "task_id":    task.ID,
                "request_id": requestID,
            }).Error("failed to publish task.created event")
        }
    }()
}
```

**Важные моменты:**
- Публикация происходит в отдельной горутине, чтобы не задерживать ответ клиенту
- Используется контекст с таймаутом 2 секунды
- При ошибке только логируем, но не возвращаем ошибку клиенту

---

## Consumer: worker для обработки сообщений

### Общая логика работы

Worker при запуске:
1. Подключается к RabbitMQ
2. Объявляет очередь `task_events` (durable)
3. Настраивает prefetch для контроля нагрузки
4. Начинает потреблять сообщения
5. Для каждого сообщения: логирует и отправляет ack

### Код потребителя

**`internal/rabbit/consumer.go`** — основной цикл обработки:

```go
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
    var event TaskEvent
    if err := json.Unmarshal(msg.Body, &event); err != nil {
        c.logger.WithError(err).Error("Failed to unmarshal message")
        msg.Nack(false, false) // не requeue, отбрасываем
        return
    }

    c.logger.WithFields(logrus.Fields{
        "event":      event.Event,
        "task_id":    event.TaskID,
        "title":      event.Title,
        "request_id": event.RequestID,
    }).Info("✅ Received task event")

    // Подтверждаем успешную обработку
    if err := msg.Ack(false); err != nil {
        c.logger.WithError(err).Error("Failed to ack message")
    }
}
```

### Prefetch

В конфигурации consumer'а установлен `prefetch = 1`:

```go
err = ch.Qos(
    prefetchCount, // prefetch count = 1
    0,             // prefetch size
    false,         // global
)
```

Это гарантирует, что worker будет получать не более одного сообщения одновременно, что предотвращает перегрузку при медленной обработке.

### Подтверждение обработки (ack)

- **Ack** отправляется **после успешной обработки** сообщения
- Если обработка не удалась (например, невалидный JSON), отправляется **Nack без requeue**
- Это гарантирует, что сообщение не потеряется и не зациклится при ошибках

---

## Запуск системы

### Предварительные требования

- Docker и Docker Compose
- Git
- Postman (для тестирования)

### Запуск всех сервисов

```bash
# Клонирование репозитория
git clone <url-репозитория>
cd MIREA-TIP-Practice-29/tech-ip-sem2/deploy

# Запуск всех сервисов
docker-compose down -v
docker-compose build --no-cache
docker-compose up -d

# Проверка статуса
docker-compose ps
```


### Просмотр логов worker

```bash
# В реальном времени
docker-compose logs -f worker

# Последние 50 строк
docker-compose logs --tail 50 worker
```

### Переменные окружения

**Для tasks сервиса:**
- `RABBITMQ_URL`: amqp://guest:guest@rabbitmq:5672/
- `RABBITMQ_QUEUE`: task_events

**Для worker сервиса:**
- `RABBITMQ_URL`: amqp://guest:guest@rabbitmq:5672/
- `RABBITMQ_QUEUE`: task_events
- `RABBITMQ_PREFETCH`: 1
- `LOG_LEVEL`: debug

---

## Скриншоты выполнения

### 1. Запуск всех сервисов через docker-compose

![img.png](about/img.png)

### 2. Логи worker до обработки сообщений

![img_1.png](about/img_1.png)

### 3. Создание задачи через Postman

![img_2.png](about/img_2.png)

### 4. Логи worker после получения сообщения

![img_3.png](about/img_3.png)

### 5. Проверка созданной задачи через GET /v1/tasks

![img_4.png](about/img_4.png)


### 6. Логи tasks сервиса с информацией о публикации

![img_5.png](about/img_5.png)

---

## Выводы

В ходе выполнения практического занятия №13 были достигнуты следующие результаты:

### Реализованная функциональность

1. **Поднят RabbitMQ** через docker-compose с persistence и management UI
2. **Создан producer** в сервисе tasks, публикующий события после создания задачи
3. **Создан отдельный сервис worker** (consumer), обрабатывающий сообщения из очереди
4. **Настроено подтверждение обработки (ack)** для гарантии доставки
5. **Реализован prefetch** для контроля нагрузки на consumer
6. **Использован формат JSON** для событий с метаданными (event, task_id, ts, request_id)

### Ключевые особенности реализации

- **Best effort публикация** — при недоступности RabbitMQ задача всё равно создаётся
- **Асинхронность** — публикация не блокирует ответ клиенту
- **Сквозная трассировка** — request_id передаётся через всё событие
- **Durable очередь** — сообщения сохраняются при рестарте брокера
- **Persistent сообщения** — записываются на диск для надёжности

### Преимущества использования брокера сообщений

1. **Развязывание сервисов** — tasks не зависит от наличия consumer'а
2. **Асинхронность** — задача создаётся быстро, обработка происходит позже
3. **Надёжность** — очередь хранит сообщения до обработки
4. **Масштабирование** — можно запустить несколько worker'ов
5. **Отказоустойчивость** — при падении worker'а сообщения не теряются

---

## Контрольные вопросы

### 1. Зачем нужен брокер сообщений, если есть HTTP?

**HTTP** — синхронный протокол "запрос-ответ". Если сервис B временно недоступен, сервис A не может отправить запрос и должен либо ждать, либо возвращать ошибку.

**Брокер сообщений** решает эту проблему:
- **Асинхронность** — отправитель не ждёт ответа
- **Буферизация** — очередь хранит сообщения, если получатель недоступен
- **Надёжность** — сообщения сохраняются на диск
- **Масштабирование** — можно добавить несколько потребителей
- **Развязывание** — сервисы не знают друг о друге напрямую

Пример: при создании задачи нужно отправить email, обновить поисковый индекс, вызвать внешний API. Если делать это синхронно через HTTP, создание задачи будет долгим и ненадёжным.

### 2. Что такое ack и зачем он нужен?

**Ack (acknowledgment)** — подтверждение от потребителя, что сообщение успешно обработано.

В RabbitMQ есть два режима:
- **auto-ack** — сообщение считается обработанным сразу после отправки потребителю
- **manual-ack** — потребитель сам отправляет подтверждение после обработки

**Зачем нужен manual-ack:**
- Гарантия, что сообщение не потеряется при падении потребителя
- Возможность вернуть сообщение в очередь при ошибке (nack с requeue)
- Контроль над жизненным циклом сообщения

Если потребитель умер, не отправив ack, сообщение вернётся в очередь и будет доставлено другому потребителю.

### 3. Почему возможна повторная доставка сообщения?

Повторная доставка (redelivery) возможна в нескольких случаях:

1. **Отсутствие ack** — потребитель получил сообщение, но не отправил ack (упал, завис, слишком долго обрабатывал)
2. **Nack с requeue** — потребитель явно вернул сообщение в очередь (например, при временной ошибке)
3. **Сетевые проблемы** — разрыв соединения до отправки ack
4. **Рестарт брокера** — если сообщения persistent, они восстановятся и будут доставлены заново

**Важно:** приложение-потребитель должно быть идемпотентным, т.е. обработка одного сообщения дважды не должна нарушать целостность данных.

### 4. Что делает prefetch?

**Prefetch** — настройка, определяющая, сколько сообщений может быть отправлено потребителю без подтверждения.

Пример с `prefetch = 1`:
1. RabbitMQ отправляет сообщение потребителю
2. Потребитель обрабатывает его
3. Потребитель отправляет ack
4. RabbitMQ отправляет следующее сообщение

**Зачем нужен prefetch:**
- Предотвращает перегрузку потребителя
- Позволяет равномерно распределять нагрузку
- Экономит память (сообщения не накапливаются у потребителя)

Если у потребителя тяжёлая обработка (например, запрос к БД или внешнему API), prefetch должен быть небольшим (1-5). Если обработка лёгкая, можно увеличить для производительности.

### 5. Чем очередь durable отличается от non-durable?

**Durable очередь:**
- Сохраняется на диске
- Переживает рестарт RabbitMQ
- Метаданные очереди не теряются
- Сообщения могут быть persistent (тоже сохраняются)

**Non-durable очередь:**
- Хранится только в памяти
- Исчезает после рестарта брокера
- Подходит для временных данных

**В проекте использована durable очередь** с persistent сообщениями, чтобы гарантировать, что события о создании задач не потеряются даже при падении RabbitMQ.