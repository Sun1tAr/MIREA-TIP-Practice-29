package service

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/shared/middleware"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/cache"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/models"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/rabbit"
	"github.com/sun1tar/MIREA-TIP-Practice-29/tech-ip-sem2/tasks/internal/repository"
)

type TaskService struct {
	repo     repository.TaskRepository
	cache    *cache.Client
	producer *rabbit.Producer
	log      *logrus.Logger
}

func NewTaskService(repo repository.TaskRepository, cacheClient *cache.Client, producer *rabbit.Producer, logger *logrus.Logger) *TaskService {
	return &TaskService{
		repo:     repo,
		cache:    cacheClient,
		producer: producer,
		log:      logger,
	}
}

// sanitizeInput - простая защита от XSS (замена опасных символов)
func sanitizeInput(input string) string {
	replacer := strings.NewReplacer(
		"<", "&lt;",
		">", "&gt;",
		"&", "&amp;",
		"\"", "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(input)
}

func (s *TaskService) Create(ctx context.Context, title, description, dueDate string) (*models.Task, error) {
	// Санитизация входных данных
	title = sanitizeInput(title)
	description = sanitizeInput(description)

	task := &models.Task{
		ID:          "t_" + uuid.New().String(),
		Title:       title,
		Description: description,
		DueDate:     dueDate,
		Done:        false,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.Create(ctx, task); err != nil {
		return nil, err
	}

	// Публикуем событие в RabbitMQ (best effort)
	if s.producer != nil {
		requestID := middleware.GetRequestID(ctx)

		// Используем отдельную горутину, чтобы не задерживать ответ
		go func() {
			// Создаём новый контекст с таймаутом для публикации
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

	// Инвалидация кэша списка, так как появилась новая задача
	go func() {
		if err := s.cache.InvalidateList(context.Background()); err != nil {
			s.log.WithError(err).Warn("failed to invalidate list cache after create")
		} else {
			s.log.WithField("task_id", task.ID).Debug("list cache invalidated after create")
		}
	}()

	s.log.WithFields(logrus.Fields{
		"task_id":    task.ID,
		"title":      task.Title,
		"request_id": middleware.GetRequestID(ctx),
	}).Info("task created successfully")

	return task, nil
}

func (s *TaskService) GetByID(ctx context.Context, id string) (*models.Task, error) {
	// Пытаемся получить из кэша
	task, err := s.cache.GetTask(ctx, id)
	if err != nil {
		// Ошибка Redis, логируем и идём в БД (деградация)
		s.log.WithError(err).WithField("task_id", id).Warn("redis error, falling back to database")
		return s.getFromDB(ctx, id)
	}

	if task != nil {
		// Cache hit
		s.log.WithField("task_id", id).Debug("CACHE HIT - task retrieved from Redis")
		return task, nil
	}

	// Cache miss - идём в БД
	s.log.WithField("task_id", id).Debug("CACHE MISS - fetching from database")
	task, err = s.getFromDB(ctx, id)
	if err != nil {
		return nil, err
	}

	// Сохраняем в кэш в фоне (не блокируем ответ)
	go func() {
		if err := s.cache.SetTask(context.Background(), task); err != nil {
			s.log.WithError(err).WithField("task_id", id).Warn("failed to set cache")
		} else {
			s.log.WithField("task_id", id).Debug("CACHE SET - task saved to Redis")
		}
	}()

	return task, nil
}

// getFromDB получает задачу из БД
func (s *TaskService) getFromDB(ctx context.Context, id string) (*models.Task, error) {
	task, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, sql.ErrNoRows
	}
	return task, nil
}

func (s *TaskService) List(ctx context.Context) ([]*models.Task, error) {
	s.log.Debug("listing tasks from database")
	return s.repo.List(ctx)
}

func (s *TaskService) Update(ctx context.Context, id string, title, description, dueDate *string, done *bool) (*models.Task, error) {
	// Сначала получаем существующую задачу
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, sql.ErrNoRows
	}

	// Обновляем поля с санитизацией
	if title != nil {
		existing.Title = sanitizeInput(*title)
	}
	if description != nil {
		existing.Description = sanitizeInput(*description)
	}
	if dueDate != nil {
		existing.DueDate = *dueDate
	}
	if done != nil {
		existing.Done = *done
	}
	existing.UpdatedAt = time.Now()

	// Сохраняем в БД
	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}

	// Инвалидация кэша в фоне
	go func() {
		if err := s.cache.DeleteTask(context.Background(), id); err != nil {
			s.log.WithError(err).WithField("task_id", id).Warn("failed to delete task from cache")
		} else {
			s.log.WithField("task_id", id).Debug("task cache invalidated after update")
		}

		if err := s.cache.InvalidateList(context.Background()); err != nil {
			s.log.WithError(err).Warn("failed to invalidate list cache after update")
		} else {
			s.log.Debug("list cache invalidated after update")
		}
	}()

	s.log.WithFields(logrus.Fields{
		"task_id":    id,
		"request_id": middleware.GetRequestID(ctx),
	}).Info("task updated successfully")

	return existing, nil
}

func (s *TaskService) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}

	// Инвалидация кэша в фоне
	go func() {
		if err := s.cache.DeleteTask(context.Background(), id); err != nil {
			s.log.WithError(err).WithField("task_id", id).Warn("failed to delete task from cache")
		} else {
			s.log.WithField("task_id", id).Debug("task cache invalidated after delete")
		}

		if err := s.cache.InvalidateList(context.Background()); err != nil {
			s.log.WithError(err).Warn("failed to invalidate list cache after delete")
		} else {
			s.log.Debug("list cache invalidated after delete")
		}
	}()

	s.log.WithFields(logrus.Fields{
		"task_id":    id,
		"request_id": middleware.GetRequestID(ctx),
	}).Info("task deleted successfully")

	return nil
}

func (s *TaskService) SearchByTitle(ctx context.Context, query string, unsafe bool) ([]*models.Task, error) {
	// Поиск не кэшируем, так как запросы разнообразные
	if unsafe {
		if postgresRepo, ok := s.repo.(*repository.PostgresTaskRepository); ok {
			s.log.WithField("query", query).Warn("⚠️ UNSAFE SEARCH MODE - SQL INJECTION POSSIBLE")
			return postgresRepo.SearchByTitleUnsafe(ctx, query)
		}
	}
	query = sanitizeInput(query)
	s.log.WithField("query", query).Debug("searching tasks in database")
	return s.repo.SearchByTitle(ctx, query)
}
