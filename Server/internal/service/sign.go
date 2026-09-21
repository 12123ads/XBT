package service

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type signClient interface {
	PreSign(mobile, password string, fixed xxt.FixedParams, code, enc string) error
	Sign(mobile, password string, fixed xxt.FixedParams, signType int, special map[string]interface{}) (string, error)
	HasActivityInCourse(mobile, password string, courseID, classID, activityID int64) (bool, error)
}

type SignService struct {
	db                  *gorm.DB
	xxt                 signClient
	cc                  *CredentialCrypto
	courseWebhook       *EnterpriseWechatWebhookNotifier
	courseNotifyMu      sync.Mutex
	courseNotifyBatches map[courseSignNotificationKey]*courseSignNotificationBatch
}

func NewSignService(db *gorm.DB, xxtClient signClient, cc *CredentialCrypto, courseWebhook *EnterpriseWechatWebhookNotifier) *SignService {
	return &SignService{
		db:                  db,
		xxt:                 xxtClient,
		cc:                  cc,
		courseWebhook:       courseWebhook,
		courseNotifyBatches: map[courseSignNotificationKey]*courseSignNotificationBatch{},
	}
}

const courseSignNotificationDebounce = 8 * time.Second

type courseSignNotificationKey struct {
	ActivityID int64
	CourseID   int64
	ClassID    int64
	SignType   int
}

type courseSignNotificationBatch struct {
	Records []model.SignRecord
	Version int
	Timer   *time.Timer
}

type ExecuteSignRequest struct {
	ActivityID    int64
	TargetUID     int64
	SignType      int
	CourseID      int64
	ClassID       int64
	IfRefreshEWM  bool
	ActivityName  string
	CourseName    string
	CourseTeacher string
	Special       map[string]interface{}
}

type SignCheckItem struct {
	UserID           int64  `json:"user_id"`
	Signed           bool   `json:"signed"`
	RecordSource     int64  `json:"record_source"`
	RecordSourceName string `json:"record_source_name"`
	Message          string `json:"message"`
}

type SignExecuteResult struct {
	UserID           int64  `json:"user_id"`
	Success          bool   `json:"success"`
	AlreadySigned    bool   `json:"already_signed"`
	RecordSource     int64  `json:"record_source"`
	RecordSourceName string `json:"record_source_name"`
	Message          string `json:"message"`
}

func (s *SignService) CheckSignStates(operatorUID, activityID, courseID, classID int64, userIDs []int64) ([]SignCheckItem, error) {
	targets := make([]int64, 0, len(userIDs)+1)
	targets = append(targets, operatorUID)
	targets = append(targets, userIDs...)
	uniq := dedupeUIDs(targets)
	if err := s.AuthorizeTargets(operatorUID, activityID, courseID, classID, uniq); err != nil {
		return nil, err
	}
	items := make([]SignCheckItem, 0, len(uniq))
	for _, uid := range uniq {
		state, err := s.resolveSignState(activityID, uid)
		if err != nil {
			return nil, err
		}
		items = append(items, state)
	}
	return items, nil
}

func (s *SignService) AuthorizeTargets(operatorUID, activityID, courseID, classID int64, targetUIDs []int64) error {
	operator, err := LoadActiveUser(s.db, operatorUID)
	if err != nil {
		return err
	}
	if activityID <= 0 {
		return ErrSignForbidden
	}
	selected, err := UserSelectedCourse(s.db, operatorUID, courseID, classID)
	if err != nil {
		return err
	}
	if !selected {
		return ErrSignForbidden
	}
	for _, uid := range targetUIDs {
		if uid <= 0 {
			return ErrSignForbidden
		}
	}
	targets := dedupeUIDs(targetUIDs)
	if len(targets) > 0 {
		var count int64
		if err := s.db.Table("user_courses uc").
			Joins("JOIN users u ON u.uid = uc.user_uid").
			Joins("JOIN whitelists w ON w.mobile = u.mobile").
			Where("uc.course_id = ? AND uc.class_id = ? AND uc.is_selected = true", courseID, classID).
			Where("u.uid IN ? AND u.permission > 0 AND w.permission > 0", targets).
			Distinct("u.uid").Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(targets)) {
			return ErrSignForbidden
		}
	}
	var count int64
	if err := s.db.Model(&model.SignActivityScope{}).
		Where("activity_id = ? AND course_id = ? AND class_id = ?", activityID, courseID, classID).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	password, err := s.cc.Decrypt(operator.CredentialCipher)
	if err != nil {
		return fmt.Errorf("%w: operator credential unavailable", ErrActivityScopeUnavailable)
	}
	found, err := s.xxt.HasActivityInCourse(operator.Mobile, password, courseID, classID, activityID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrActivityScopeUnavailable, err)
	}
	if !found {
		return ErrSignForbidden
	}
	scope := model.SignActivityScope{ActivityID: activityID, CourseID: courseID, ClassID: classID}
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&scope).Error
}

func (s *SignService) ExecuteOne(operatorUID int64, req ExecuteSignRequest) (SignExecuteResult, error) {
	if err := s.AuthorizeTargets(operatorUID, req.ActivityID, req.CourseID, req.ClassID, []int64{req.TargetUID}); err != nil {
		return SignExecuteResult{}, err
	}
	state, err := s.resolveSignState(req.ActivityID, req.TargetUID)
	if err != nil {
		return SignExecuteResult{}, err
	}
	if state.Signed {
		return SignExecuteResult{
			UserID:           req.TargetUID,
			Success:          true,
			AlreadySigned:    true,
			RecordSource:     state.RecordSource,
			RecordSourceName: state.RecordSourceName,
			Message:          state.Message,
		}, nil
	}

	target, err := LoadActiveUser(s.db, req.TargetUID)
	if err != nil {
		if errors.Is(err, ErrAccountInactive) {
			return SignExecuteResult{}, ErrSignForbidden
		}
		return SignExecuteResult{}, err
	}
	password, err := s.cc.Decrypt(target.CredentialCipher)
	if err != nil {
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "该同学登录信息已过期，请先重新登录"}, nil
	}

	fixed := xxt.FixedParams{
		ActiveID:     req.ActivityID,
		UID:          req.TargetUID,
		CourseID:     req.CourseID,
		ClassID:      req.ClassID,
		IfRefreshEWM: req.IfRefreshEWM,
	}
	if req.SignType == xxt.SignQRCode {
		enc, _ := req.Special["enc"].(string)
		code, _ := req.Special["c"].(string)
		if err := s.xxt.PreSign(target.Mobile, password, fixed, code, enc); err != nil {
			return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: "预签到失败，请重试"}, nil
		}
	}

	result, err := s.xxt.Sign(target.Mobile, password, fixed, req.SignType, req.Special)
	if err != nil {
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: s.toUserSignMessage(err.Error())}, nil
	}
	result = strings.TrimSpace(result)
	if result != "success" {
		if strings.Contains(result, "您已签到过了") {
			return SignExecuteResult{
				UserID:           req.TargetUID,
				Success:          true,
				AlreadySigned:    true,
				RecordSource:     -1,
				RecordSourceName: "学习通",
				Message:          "该同学已在学习通签到",
			}, nil
		}
		return SignExecuteResult{UserID: req.TargetUID, Success: false, Message: s.toUserSignMessage(result)}, nil
	}

	rec := s.signRecordFromRequest(req, operatorUID)
	dbResult := s.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_uid"}, {Name: "activity_id"}}, DoNothing: true}).Create(&rec)
	if dbResult.Error != nil {
		return SignExecuteResult{}, dbResult.Error
	}

	sourceName := s.getSourceName(operatorUID)
	if strings.TrimSpace(sourceName) == "" {
		sourceName = "未知用户"
	}
	if dbResult.RowsAffected > 0 {
		s.enqueueCourseSignSuccess(rec)
	}
	return SignExecuteResult{
		UserID:           req.TargetUID,
		Success:          true,
		AlreadySigned:    false,
		RecordSource:     operatorUID,
		RecordSourceName: sourceName,
		Message:          "签到成功",
	}, nil
}

func (s *SignService) enqueueCourseSignSuccess(rec model.SignRecord) {
	if !s.courseWebhook.Enabled() {
		return
	}
	key := courseSignNotificationKey{
		ActivityID: rec.ActivityID,
		CourseID:   rec.CourseID,
		ClassID:    rec.ClassID,
		SignType:   rec.SignType,
	}

	s.courseNotifyMu.Lock()
	batch := s.courseNotifyBatches[key]
	if batch == nil {
		batch = &courseSignNotificationBatch{}
		s.courseNotifyBatches[key] = batch
	}
	batch.Records = append(batch.Records, rec)
	batch.Version++
	version := batch.Version
	if batch.Timer != nil {
		batch.Timer.Stop()
	}
	batch.Timer = time.AfterFunc(courseSignNotificationDebounce, func() {
		s.flushCourseSignNotification(key, version)
	})
	s.courseNotifyMu.Unlock()
}

func (s *SignService) flushCourseSignNotification(key courseSignNotificationKey, version int) {
	s.courseNotifyMu.Lock()
	batch := s.courseNotifyBatches[key]
	if batch == nil || batch.Version != version {
		s.courseNotifyMu.Unlock()
		return
	}
	records := append([]model.SignRecord(nil), batch.Records...)
	delete(s.courseNotifyBatches, key)
	s.courseNotifyMu.Unlock()

	if len(records) == 0 || !s.courseWebhook.Enabled() {
		return
	}
	content, err := s.courseSignSummaryMarkdown(records)
	if err != nil {
		log.Printf("course sign webhook summary failed: %v", err)
		return
	}
	s.courseWebhook.SendMarkdownAsync("course sign", content)
}

func (s *SignService) courseSignSummaryMarkdown(records []model.SignRecord) (string, error) {
	users, err := s.courseSignUserMap(records)
	if err != nil {
		return "", err
	}

	first := records[0]
	courseName := webhookText(first.CourseName, "未知课程")
	activityName := webhookText(first.ActivityName, "未知活动")
	targetNames := make([]string, 0, len(records))
	sourceNames := make([]string, 0, len(records))
	firstTime := records[0].SignTimeMS
	lastTime := records[0].SignTimeMS
	for _, rec := range records {
		targetNames = append(targetNames, courseSignUserName(rec.UserUID, users))
		sourceNames = append(sourceNames, courseSignUserName(rec.SourceUID, users))
		if rec.SignTimeMS < firstTime {
			firstTime = rec.SignTimeMS
		}
		if rec.SignTimeMS > lastTime {
			lastTime = rec.SignTimeMS
		}
		if strings.TrimSpace(courseName) == "" || courseName == "未知课程" {
			courseName = webhookText(rec.CourseName, "未知课程")
		}
		if strings.TrimSpace(activityName) == "" || activityName == "未知活动" {
			activityName = webhookText(rec.ActivityName, "未知活动")
		}
	}

	timeText := time.UnixMilli(lastTime).Format("2006-01-02 15:04:05")
	if firstTime != lastTime {
		timeText = fmt.Sprintf("%s - %s", time.UnixMilli(firstTime).Format("2006-01-02 15:04:05"), time.UnixMilli(lastTime).Format("15:04:05"))
	}

	return strings.Join([]string{
		"### 课程签到成功",
		fmt.Sprintf(">课程：%s", courseName),
		fmt.Sprintf(">活动：%s", activityName),
		fmt.Sprintf(">结果：成功 %d 人", len(records)),
		fmt.Sprintf(">签到用户：%s", joinWebhookValues(targetNames, 20)),
		fmt.Sprintf(">执行人：%s", joinWebhookValues(sourceNames, 8)),
		fmt.Sprintf(">签到类型：%s", signTypeLabel(first.SignType)),
		fmt.Sprintf(">时间：%s", timeText),
		fmt.Sprintf(">活动 ID：%d", first.ActivityID),
		fmt.Sprintf(">课程/班级：%d / %d", first.CourseID, first.ClassID),
	}, "\n"), nil
}

func (s *SignService) courseSignUserMap(records []model.SignRecord) (map[int64]model.User, error) {
	seen := map[int64]struct{}{}
	uids := make([]int64, 0, len(records)*2)
	for _, rec := range records {
		for _, uid := range []int64{rec.UserUID, rec.SourceUID} {
			if uid <= 0 {
				continue
			}
			if _, ok := seen[uid]; ok {
				continue
			}
			seen[uid] = struct{}{}
			uids = append(uids, uid)
		}
	}
	if len(uids) == 0 {
		return map[int64]model.User{}, nil
	}
	var users []model.User
	if err := s.db.Where("uid IN ?", uids).Find(&users).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]model.User, len(users))
	for _, user := range users {
		out[user.UID] = user
	}
	return out, nil
}

func courseSignUserName(uid int64, users map[int64]model.User) string {
	if user, ok := users[uid]; ok {
		return webhookText(user.Name, fmt.Sprintf("UID %d", uid))
	}
	return fmt.Sprintf("UID %d", uid)
}

func signTypeLabel(signType int) string {
	switch signType {
	case xxt.SignNormal:
		return "普通签到"
	case xxt.SignQRCode:
		return "二维码签到"
	case xxt.SignGesture:
		return "手势签到"
	case xxt.SignLocation:
		return "位置签到"
	case xxt.SignCode:
		return "签到码签到"
	default:
		return fmt.Sprintf("未知类型 %d", signType)
	}
}

func (s *SignService) signRecordFromRequest(req ExecuteSignRequest, sourceUID int64) model.SignRecord {
	return model.SignRecord{
		UserUID:       req.TargetUID,
		ActivityID:    req.ActivityID,
		SourceUID:     sourceUID,
		CourseID:      req.CourseID,
		ClassID:       req.ClassID,
		SignType:      req.SignType,
		ActivityName:  strings.TrimSpace(req.ActivityName),
		CourseName:    strings.TrimSpace(req.CourseName),
		CourseTeacher: strings.TrimSpace(req.CourseTeacher),
		SignTimeMS:    time.Now().UnixMilli(),
	}
}

func (s *SignService) toUserSignMessage(raw string) string {
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return "签到失败，请稍后重试"
	}

	lower := strings.ToLower(msg)
	switch {
	case msg == "validate" || strings.Contains(lower, "validate"):
		return "签到校验未通过，请重试"
	case strings.Contains(msg, "验证码识别失败") || strings.Contains(lower, "captcha"):
		return "验证码校验失败，请重试"
	case strings.Contains(msg, "缺少二维码 enc 参数"):
		return "二维码参数缺失，请刷新活动后重试"
	case strings.Contains(msg, "缺少 sign_code 参数"):
		return "签到码缺失，请输入后重试"
	case strings.Contains(msg, "请求过于频繁"):
		return "操作太频繁，请稍后再试"
	case strings.Contains(msg, "活动已结束"):
		return "该签到已结束"
	case strings.Contains(msg, "签到成功"):
		return "签到成功"
	case strings.Contains(msg, "您已签到过了"):
		return "该同学已在学习通签到"
	default:
		return msg
	}
}

func (s *SignService) resolveSignState(activityID, uid int64) (SignCheckItem, error) {
	state := SignCheckItem{UserID: uid, Signed: false, RecordSource: 0, RecordSourceName: "", Message: "未签到"}
	if activityID <= 0 || uid <= 0 {
		return state, nil
	}

	var rec model.SignRecord
	if err := s.db.Where("user_uid = ? AND activity_id = ?", uid, activityID).Take(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return state, nil
		}
		return SignCheckItem{}, err
	}

	state.Signed = true
	state.RecordSource = rec.SourceUID
	if rec.SourceUID == -1 {
		state.RecordSourceName = "学习通"
		state.Message = "该同学已在学习通签到"
		return state, nil
	}
	if rec.SourceUID == uid {
		state.RecordSourceName = s.getSourceName(uid)
		if state.RecordSourceName == "" {
			state.RecordSourceName = "本人"
		}
		state.Message = "该同学已本人签到"
		return state, nil
	}
	state.RecordSourceName = s.getSourceName(rec.SourceUID)
	if state.RecordSourceName == "" {
		state.RecordSourceName = "未知用户"
	}
	state.Message = fmt.Sprintf("该同学已被%s代签", state.RecordSourceName)
	return state, nil
}

func (s *SignService) getSourceName(sourceUID int64) string {
	if sourceUID <= 0 {
		return ""
	}
	var user model.User
	if err := s.db.Where("uid = ?", sourceUID).Take(&user).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(user.Name)
}

func dedupeUIDs(userIDs []int64) []int64 {
	set := make(map[int64]struct{}, len(userIDs))
	out := make([]int64, 0, len(userIDs))
	for _, uid := range userIDs {
		if uid <= 0 {
			continue
		}
		if _, ok := set[uid]; ok {
			continue
		}
		set[uid] = struct{}{}
		out = append(out, uid)
	}
	return out
}
