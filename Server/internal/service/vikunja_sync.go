package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"xbt2/server/internal/config"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

const vikunjaSyncRunAt = "07:00"

type learningHomeworkClient interface {
	GetLearningHomeworkFor(mobile, password string) ([]xxt.LearningItem, error)
}

type VikunjaSyncService struct {
	db      *gorm.DB
	xxt     learningHomeworkClient
	cc      *CredentialCrypto
	baseURL string
	loc     *time.Location
	mu      sync.Mutex
}

func NewVikunjaSyncService(db *gorm.DB, xxtClient learningHomeworkClient, cc *CredentialCrypto, baseURL string) (*VikunjaSyncService, error) {
	baseURL, err := config.NormalizeVikunjaBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	return &VikunjaSyncService{db: db, xxt: xxtClient, cc: cc, baseURL: baseURL, loc: loc}, nil
}

type VikunjaSyncResult struct {
	Created int    `json:"created"`
	Updated int    `json:"updated"`
	Total   int    `json:"total"`
	Message string `json:"message"`
}

// SyncUser 将该账号学习仪表盘中未提交的作业 upsert 到其配置的 Vikunja 项目。
func (s *VikunjaSyncService) SyncUser(ctx context.Context, uid int64) (result *VikunjaSyncResult, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.baseURL == "" {
		return nil, ErrVikunjaNotConfigured
	}
	settings, err := s.loadSettings(ctx, uid)
	if err != nil {
		return nil, err
	}

	result = &VikunjaSyncResult{}
	failed := 0
	defer func() {
		err = errors.Join(err, s.checkSyncState(ctx, settings))
		if err == nil {
			result.Message = fmt.Sprintf("同步完成：未提交作业 %d 条，新建 %d 条，更新 %d 条", result.Total, result.Created, result.Updated)
		} else if errors.Is(err, xxt.ErrCaptchaRequired) {
			result.Message = "学习通触发反爬验证，本次同步未完成"
		} else {
			result.Message = fmt.Sprintf("同步未完成：未提交作业 %d 条，新建 %d 条，更新 %d 条，失败 %d 条", result.Total, result.Created, result.Updated, failed)
		}
		if recordErr := s.recordSync(settings, result.Message); recordErr != nil {
			err = errors.Join(err, recordErr)
			result.Message = fmt.Sprintf("同步未完成：新建 %d 条，更新 %d 条，同步状态保存失败", result.Created, result.Updated)
		}
	}()
	if err := s.checkSyncState(ctx, settings); err != nil {
		return result, err
	}
	token, err := s.cc.Decrypt(settings.APITokenCipher)
	if err != nil || strings.TrimSpace(token) == "" {
		return result, ErrVikunjaRebindRequired
	}
	client, err := NewVikunjaClient(s.baseURL, token)
	if err != nil {
		return result, err
	}
	client.beforeRequest = func(requestCtx context.Context) error {
		return s.checkSyncState(requestCtx, settings)
	}
	mobile, password, err := s.loadUserCredential(ctx, uid)
	if err != nil {
		return result, err
	}
	if err := s.checkSyncState(ctx, settings); err != nil {
		return result, err
	}
	homework, fetchErr := s.xxt.GetLearningHomeworkFor(mobile, password)
	if err := s.checkSyncState(ctx, settings); err != nil {
		return result, err
	}
	if fetchErr != nil {
		return result, fmt.Errorf("获取作业列表失败: %w", fetchErr)
	}
	for _, item := range homework {
		if item.Pending {
			result.Total++
		}
	}
	labelCache := map[string]int64{}
	var failures []error
	for _, item := range homework {
		if !item.Pending {
			continue
		}
		if stateErr := s.checkSyncState(ctx, settings); stateErr != nil {
			failures = append(failures, stateErr)
			break
		}
		if itemErr := s.upsertTask(ctx, client, settings, item, labelCache, result); itemErr != nil {
			failed++
			failures = append(failures, itemErr)
			if errors.Is(itemErr, context.Canceled) || errors.Is(itemErr, context.DeadlineExceeded) || errors.Is(itemErr, ErrVikunjaSettingsChanged) || errors.Is(itemErr, ErrAccountInactive) {
				break
			}
		}
	}
	return result, errors.Join(failures...)
}

func (s *VikunjaSyncService) upsertTask(ctx context.Context, client *VikunjaClient, settings *model.VikunjaSettings, item xxt.LearningItem, labelCache map[string]int64, result *VikunjaSyncResult) error {
	itemKey := "homework:" + item.ID
	var mapping model.VikunjaSyncItem
	err := s.db.WithContext(ctx).Where("user_uid = ? AND instance_url = ? AND project_id = ? AND item_key = ?", settings.UserUID, s.baseURL, settings.ProjectID, itemKey).First(&mapping).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		titleChanged := mapping.Title != item.Title
		dueChanged := item.EndTime > 0 && mapping.DueDate != item.EndTime
		if !titleChanged && !dueChanged {
			return nil
		}
		task, err := client.GetTask(ctx, mapping.VikunjaTaskID)
		if err != nil {
			return err
		}
		var projectID int64
		if json.Unmarshal(task["project_id"], &projectID) != nil || projectID != settings.ProjectID {
			return &VikunjaRequestError{method: http.MethodGet, path: fmt.Sprintf("/tasks/%d", mapping.VikunjaTaskID), cause: errors.New("task belongs to another project")}
		}
		updates := map[string]interface{}{"last_seen_at": time.Now()}
		if titleChanged {
			task["title"], _ = json.Marshal(item.Title)
			updates["title"] = item.Title
		}
		if dueChanged {
			task["due_date"], _ = json.Marshal(time.UnixMilli(item.EndTime).UTC().Format(time.RFC3339))
			updates["due_date"] = item.EndTime
		}
		if err := client.UpdateTask(ctx, mapping.VikunjaTaskID, task); err != nil {
			return err
		}
		updated := s.db.WithContext(ctx).Model(&mapping).Where(
			"user_uid = ? AND instance_url = ? AND project_id = ? AND item_key = ?",
			settings.UserUID, s.baseURL, settings.ProjectID, itemKey,
		).Updates(updates)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return errors.New("Vikunja 任务映射已变化")
		}
		result.Updated++
		return nil
	}

	labelID, err := s.ensureCourseLabel(ctx, client, item.CourseName, labelCache)
	if err != nil {
		return err
	}
	task := &VikunjaTask{
		Title:       item.Title,
		Description: vikunjaTaskDescription(item),
		ProjectID:   settings.ProjectID,
	}
	if item.EndTime > 0 {
		due := time.UnixMilli(item.EndTime)
		task.DueDate = &due
	}
	created, err := client.CreateTask(ctx, settings.ProjectID, task)
	if err != nil {
		return err
	}
	mapping = model.VikunjaSyncItem{
		UserUID:       settings.UserUID,
		InstanceURL:   s.baseURL,
		ItemKey:       itemKey,
		VikunjaTaskID: created.ID,
		ProjectID:     settings.ProjectID,
		Title:         item.Title,
		LastSeenAt:    time.Now(),
	}
	if item.EndTime > 0 {
		mapping.DueDate = item.EndTime
	}
	// 先保存已创建的任务映射；后续标签失败也不能在下次同步重复创建任务。
	if err := s.db.WithContext(ctx).Create(&mapping).Error; err != nil {
		return err
	}
	if labelID > 0 {
		if err := client.AttachLabel(ctx, created.ID, labelID); err != nil {
			return err
		}
	}
	result.Created++
	return nil
}

func (s *VikunjaSyncService) ensureCourseLabel(ctx context.Context, client *VikunjaClient, courseName string, labelCache map[string]int64) (int64, error) {
	courseName = strings.TrimSpace(courseName)
	if courseName == "" {
		return 0, nil
	}
	if id, ok := labelCache[courseName]; ok {
		return id, nil
	}
	id, err := client.EnsureLabel(ctx, courseName)
	if err != nil {
		return 0, err
	}
	labelCache[courseName] = id
	return id, nil
}

func vikunjaTaskDescription(item xxt.LearningItem) string {
	var b strings.Builder
	if item.CourseName != "" {
		b.WriteString("课程：" + item.CourseName + "\n")
	}
	if item.Status != "" {
		b.WriteString("状态：" + item.Status + "\n")
	}
	if item.Info != "" {
		b.WriteString("时间：" + item.Info + "\n")
	}
	if item.Link != "" {
		b.WriteString("\n[去学习通处理](" + item.Link + ")")
	}
	return b.String()
}

func (s *VikunjaSyncService) loadSettings(ctx context.Context, uid int64) (*model.VikunjaSettings, error) {
	var settings model.VikunjaSettings
	if err := s.db.WithContext(ctx).Where("user_uid = ?", uid).First(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrVikunjaRebindRequired
		}
		return nil, err
	}
	if settings.BoundInstanceURL != s.baseURL || settings.APITokenCipher == "" {
		return nil, ErrVikunjaRebindRequired
	}
	if !settings.Enabled {
		return nil, ErrVikunjaSyncDisabled
	}
	if settings.ProjectID <= 0 {
		return nil, ErrVikunjaRebindRequired
	}
	return &settings, nil
}

func (s *VikunjaSyncService) loadUserCredential(ctx context.Context, uid int64) (string, string, error) {
	user, err := LoadActiveUser(s.db.WithContext(ctx), uid)
	if err != nil {
		return "", "", err
	}
	password, err := s.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		return "", "", fmt.Errorf("credential expired")
	}
	return user.Mobile, password, nil
}

func (s *VikunjaSyncService) matchingSettings(settings *model.VikunjaSettings) *gorm.DB {
	return s.db.Model(&model.VikunjaSettings{}).Where(
		"user_uid = ? AND bound_instance_url = ? AND COALESCE(api_token_cipher, '') = ? AND COALESCE(project_id, 0) = ? AND enabled = ?",
		settings.UserUID, settings.BoundInstanceURL, settings.APITokenCipher, settings.ProjectID, settings.Enabled,
	)
}

func (s *VikunjaSyncService) checkSyncState(ctx context.Context, settings *model.VikunjaSettings) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := LoadActiveUser(s.db.WithContext(ctx), settings.UserUID); err != nil {
		return err
	}
	var count int64
	if err := s.matchingSettings(settings).WithContext(ctx).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrVikunjaSettingsChanged
	}
	return nil
}

func (s *VikunjaSyncService) recordSync(settings *model.VikunjaSettings, message string) error {
	now := time.Now()
	updates := map[string]interface{}{
		"last_sync_at":      &now,
		"last_sync_message": message,
		"updated_at":        now,
	}
	updated := s.matchingSettings(settings).Updates(updates)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return ErrVikunjaSettingsChanged
	}
	return nil
}

// SyncEnabledUsers 遍历所有启用同步的账号，逐个执行每日同步。
func (s *VikunjaSyncService) SyncEnabledUsers(ctx context.Context) {
	var uids []int64
	if err := s.db.WithContext(ctx).Model(&model.VikunjaSettings{}).Where("enabled = ?", true).Pluck("user_uid", &uids).Error; err != nil {
		log.Printf("vikunja sync load users failed: %v", err)
		return
	}
	for _, uid := range uids {
		if ctx.Err() != nil {
			return
		}
		result, err := s.SyncUser(ctx, uid)
		if err != nil {
			log.Printf("vikunja scheduled sync uid=%d failed: %v", uid, err)
			continue
		}
		log.Printf("vikunja scheduled sync uid=%d: %s", uid, result.Message)
	}
}

func (s *VikunjaSyncService) NextRunAt(now time.Time) time.Time {
	localNow := now.In(s.loc)
	hour, minute := 7, 0
	if runAt := vikunjaSyncRunAt; len(runAt) >= 4 {
		fmt.Sscanf(runAt, "%d:%d", &hour, &minute)
	}
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, s.loc)
	if !localNow.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s *VikunjaSyncService) StartScheduler(ctx context.Context) {
	go func() {
		for {
			next := s.NextRunAt(time.Now())
			wait := time.Until(next)
			if wait < 0 {
				wait = 0
			}
			log.Printf("vikunja sync next run at %s", next.Format(time.RFC3339))
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				s.SyncEnabledUsers(ctx)
			}
		}
	}()
}
