package xxt

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	taskEngineSummaryType = "任务引擎"
	taskEngineDetailTTL   = 10 * time.Minute
	taskEngineDatePattern = `(?:\d{4}[-/.年]\d{1,2}[-/.月]\d{1,2}日?|\d{1,2}[-/.月]\d{1,2}日?)(?:\s+\d{1,2}:\d{2}(?::\d{2})?)?`
)

// 需要抓取截止时间的计划类型，视频/任务点等没有期限要求，跳过详情页请求。
var taskEngineDeadlineTypes = map[string]struct{}{
	"作业": {}, "考试": {}, "测验": {}, "随堂练习": {}, "分组任务": {}, "分组讨论": {},
	"签到": {}, "问卷": {}, "抢答": {}, "投票": {}, "直播": {}, "AI实践": {},
}

// 详情页只允许跟随到学习通自有域名，避免跳转到外部站点。
var taskEngineDetailHosts = map[string]struct{}{
	"mooc1.chaoxing.com": {}, "mooc2-ans.chaoxing.com": {}, "mobilelearn.chaoxing.com": {},
	"i.chaoxing.com": {}, "task.chaoxing.com": {},
}

var taskEnginePlanTypeNames = map[int64]string{
	0: "视频/任务点", 1: "视频/任务点", 4: "作业", 5: "考试", 8: "视频/任务点",
	9: "签到", 10: "视频/任务点", 11: "视频/任务点", 13: "测验", 14: "分组讨论",
	15: "AI实践", 17: "测验", 22: "视频/任务点",
}

var (
	taskEngineUserIDRe    = regexp.MustCompile(`(?:const|let|var)\s+eTaskUserId\s*=\s*["']([^"']+)["']`)
	taskEngineShortDateRe = regexp.MustCompile(`^(\d{1,2})-(\d{1,2})(?:\s+(\d{1,2}):(\d{2})(?::(\d{2}))?)?$`)
	taskEngineStartTimeRe = regexp.MustCompile(`(?:开始时间|开放时间)\s*[:：]?\s*(` + taskEngineDatePattern + `)`)
	taskEngineEndTimeRe   = regexp.MustCompile(`(?:截止时间|结束时间|截止日期|有效期至)\s*[:：]?\s*(` + taskEngineDatePattern + `)`)
	taskEnginePeriodRe    = regexp.MustCompile(`(?:作答时间|考试时间|活动时间|任务时间|学习时间|开放时间)\s*[:：]?\s*(` + taskEngineDatePattern + `)\s*(?:至|到|~|～)\s*(` + taskEngineDatePattern + `)`)
)

type taskEnginePlanDetails struct {
	TaskLink  string
	StartTime string
	EndTime   string
	Finished  bool
}

// 任务引擎明细读取开销大（每个计划最多 3 次请求），加短时缓存让看板刷新不必重复抓取。
type taskEngineDetailCache struct {
	mu      sync.Mutex
	entries map[string]taskEngineDetailEntry
}

type taskEngineDetailEntry struct {
	detail    taskEnginePlanDetails
	expiresAt time.Time
}

var globalTaskEngineDetailCache = newTaskEngineDetailCache()

func newTaskEngineDetailCache() *taskEngineDetailCache {
	return &taskEngineDetailCache{entries: make(map[string]taskEngineDetailEntry)}
}

func (cache *taskEngineDetailCache) get(key string) (taskEnginePlanDetails, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries[key]
	if !ok {
		return taskEnginePlanDetails{}, false
	}
	if time.Now().After(entry.expiresAt) {
		delete(cache.entries, key)
		return taskEnginePlanDetails{}, false
	}
	return entry.detail, true
}

func (cache *taskEngineDetailCache) set(key string, detail taskEnginePlanDetails) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	for key, entry := range cache.entries {
		if now.After(entry.expiresAt) {
			delete(cache.entries, key)
		}
	}
	cache.entries[key] = taskEngineDetailEntry{detail: detail, expiresAt: now.Add(taskEngineDetailTTL)}
}

type learningResponse struct {
	Body     string
	FinalURL string
}

func (c *Client) fetchLearningPage(cli *http.Client, method, rawURL, referer string, xhr bool) (learningResponse, error) {
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return learningResponse{}, err
	}
	req.Header.Set("User-Agent", c.mobileUA)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if xhr {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	resp, err := cli.Do(req)
	if err != nil {
		return learningResponse{}, err
	}
	defer resp.Body.Close()
	if isAntiSpiderBlocked(resp) {
		io.Copy(io.Discard, resp.Body)
		return learningResponse{}, ErrCaptchaRequired
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		io.Copy(io.Discard, resp.Body)
		return learningResponse{}, fmt.Errorf("query task engine page failed: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return learningResponse{}, err
	}
	finalURL := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return learningResponse{Body: string(body), FinalURL: finalURL}, nil
}

// getTaskEngineTasksForCourse 读取任务引擎任务包并展开为具体学习计划，
// 普通错误保留已取得的条目并回传；验证码错误优先交由调用方恢复会话。
func (c *Client) getTaskEngineTasksForCourse(cli *http.Client, course learningCourse) ([]LearningItem, error) {
	packages, err := c.fetchTaskEnginePackages(cli, course)
	if err != nil {
		return nil, err
	}
	items := make([]LearningItem, 0, len(packages))
	var errs []error
	for _, pkg := range packages {
		expanded, err := c.expandTaskEnginePackage(cli, course, pkg)
		if errors.Is(err, ErrCaptchaRequired) {
			return nil, err
		}
		items = append(items, expanded...)
		if err != nil {
			errs = append(errs, fmt.Errorf("task package %s: %w", firstNonEmpty(taskEngineNumericID(pkg["id"]), strVal(pkg["id"])), err))
		}
	}
	return compactLearningItems(items), errors.Join(errs...)
}

func (c *Client) fetchTaskEnginePackages(cli *http.Client, course learningCourse) ([]map[string]interface{}, error) {
	u := fmt.Sprintf("https://mobilelearn.chaoxing.com/v2/apis/active/getData?DB_STRATEGY=DEFAULT&courseId=%d&classId=%d", course.CourseID, course.ClassID)
	resp, err := c.fetchLearningPage(cli, http.MethodGet, u, "https://mobilelearn.chaoxing.com/", false)
	if err != nil {
		return nil, err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resp.Body), &payload); err != nil {
		return nil, err
	}
	var arr []interface{}
	if raw, ok := payload["data"]; ok {
		if err := json.Unmarshal(raw, &arr); err != nil {
			return nil, err
		}
	}
	return normalizeActiveList(arr), nil
}

func (c *Client) expandTaskEnginePackage(cli *http.Client, course learningCourse, pkg map[string]interface{}) ([]LearningItem, error) {
	taskID := strconv.FormatInt(int64FromAny(pkg["id"]), 10)
	summary := taskEngineSummaryItem(pkg, course, taskID)
	jumpLink := summary.Link
	if jumpLink == "" {
		return []LearningItem{summary}, nil
	}

	var errs []error
	landingURL := jumpLink
	taskUserID := ""
	landing, err := c.fetchLearningPage(cli, http.MethodGet, jumpLink, "https://mobilelearn.chaoxing.com/", false)
	if errors.Is(err, ErrCaptchaRequired) {
		return nil, err
	}
	if err != nil {
		errs = append(errs, fmt.Errorf("task landing: %w", err))
	} else {
		taskUserID = extractTaskEngineUserID(landing)
		landingURL = landing.FinalURL
	}
	if taskUserID == "" {
		subURL := fmt.Sprintf("https://task.chaoxing.com/userStudyPlan/studyPlanSubPage?taskId=%s&encryJumpGroupId=null", url.QueryEscape(taskID))
		sub, err := c.fetchLearningPage(cli, http.MethodGet, subURL, jumpLink, false)
		if errors.Is(err, ErrCaptchaRequired) {
			return nil, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("task subpage: %w", err))
		} else {
			taskUserID = extractTaskEngineUserID(sub)
		}
	}
	if taskUserID == "" {
		return []LearningItem{summary}, errors.Join(errs...)
	}

	plans, err := c.fetchTaskEnginePlans(cli, taskUserID, firstNonEmpty(landingURL, jumpLink))
	if errors.Is(err, ErrCaptchaRequired) {
		return nil, err
	}
	if err != nil {
		errs = append(errs, err)
	}
	if len(plans) == 0 {
		return []LearningItem{summary}, errors.Join(errs...)
	}

	items := make([]LearningItem, 0, len(plans))
	for _, plan := range plans {
		details, err := c.resolveTaskEnginePlanDetails(cli, plan, taskUserID, jumpLink)
		if errors.Is(err, ErrCaptchaRequired) {
			return nil, err
		}
		items = append(items, taskEnginePlanItem(plan, course, taskID, summary.Title, details))
		if err != nil {
			errs = append(errs, fmt.Errorf("task plan %s: %w", firstNonEmpty(taskEngineNumericID(plan["planId"]), strVal(plan["planId"])), err))
		}
	}
	return items, errors.Join(errs...)
}

func (c *Client) fetchTaskEnginePlans(cli *http.Client, taskUserID, referer string) ([]map[string]interface{}, error) {
	groupURL := fmt.Sprintf("https://task.chaoxing.com/userStudyPlan/getGroupData?encryTaskUserId=%s", url.QueryEscape(taskUserID))
	groupResp, err := c.fetchLearningPage(cli, http.MethodPost, groupURL, referer, true)
	if err != nil {
		return nil, err
	}
	groups, err := decodeTaskEngineDataArray(groupResp.Body)
	if err != nil {
		return nil, err
	}
	plans := make([]map[string]interface{}, 0, len(groups))
	var errs []error
	for _, group := range groups {
		groupID := strVal(group["encryptGroupId"])
		if groupID == "" {
			errs = append(errs, errors.New("task group missing encryptGroupId"))
			continue
		}
		planURL := fmt.Sprintf("https://task.chaoxing.com/userStudyPlan/getPlanDataByGroupId?encryTaskUserId=%s&encryGroupId=%s", url.QueryEscape(taskUserID), url.QueryEscape(groupID))
		planResp, err := c.fetchLearningPage(cli, http.MethodPost, planURL, referer, true)
		if errors.Is(err, ErrCaptchaRequired) {
			return nil, err
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("task group %s: %w", groupID, err))
			continue
		}
		data, err := decodeTaskEngineDataArray(planResp.Body)
		if err != nil {
			errs = append(errs, fmt.Errorf("task group %s: %w", groupID, err))
			continue
		}
		plans = append(plans, data...)
	}
	return plans, errors.Join(errs...)
}

func decodeTaskEngineDataArray(body string) ([]map[string]interface{}, error) {
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil, err
	}
	var arr []interface{}
	if err := json.Unmarshal(payload.Data, &arr); err != nil {
		return nil, err
	}
	return normalizeActiveList(arr), nil
}

func (c *Client) resolveTaskEnginePlanDetails(cli *http.Client, plan map[string]interface{}, taskUserID, fallbackLink string) (taskEnginePlanDetails, error) {
	planFallback := firstNonEmpty(normalizeTaskEngineStudyURL(strVal(firstNonNil(plan["hyperLink"], plan["url"])), ""), fallbackLink)
	details := taskEnginePlanDetails{TaskLink: planFallback, Finished: isTaskEngineCompletedStudyURL(planFallback)}
	encryptPlanID := strVal(plan["encryptPlanId"])
	if encryptPlanID == "" {
		return details, nil
	}
	cacheKey := taskUserID + ":" + encryptPlanID
	if cached, ok := globalTaskEngineDetailCache.get(cacheKey); ok {
		return cached, nil
	}

	studyURL := fmt.Sprintf("https://task.chaoxing.com/userStudyPlan/getToStudyUrl?encryptPlanId=%s&encryTaskUserId=%s&studyJumpType=0&isInterface=false", url.QueryEscape(encryptPlanID), url.QueryEscape(taskUserID))
	studyResp, err := c.fetchLearningPage(cli, http.MethodPost, studyURL, "https://task.chaoxing.com/", true)
	if err != nil {
		return details, err
	}
	var payload struct {
		Result bool `json:"result"`
		Data   struct {
			URL        string `json:"url"`
			DomainName string `json:"domainName"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(studyResp.Body), &payload); err != nil {
		return details, err
	}
	if !payload.Result {
		return details, errors.New("task plan details unavailable")
	}
	if direct := normalizeTaskEngineStudyURL(payload.Data.URL, payload.Data.DomainName); direct != "" {
		details.TaskLink = direct
		details.Finished = details.Finished || isTaskEngineCompletedStudyURL(direct)
		if _, needDeadline := taskEngineDeadlineTypes[taskEnginePlanTypeName(plan)]; needDeadline &&
			strVal(plan["endDateStr"]) == "" && !taskEnginePlanIsFinished(plan) && !details.Finished {
			if u, err := url.Parse(direct); err == nil {
				if _, allowed := taskEngineDetailHosts[u.Hostname()]; allowed {
					if err := c.fillTaskEngineDetailTimes(cli, direct, &details); err != nil {
						return details, err
					}
				}
			}
		}
	}
	globalTaskEngineDetailCache.set(cacheKey, details)
	return details, nil
}

func (c *Client) fillTaskEngineDetailTimes(cli *http.Client, rawURL string, details *taskEnginePlanDetails) error {
	resp, err := c.fetchLearningPage(cli, http.MethodGet, rawURL, "https://task.chaoxing.com/", false)
	if err != nil {
		return err
	}
	if final := normalizeTaskEngineStudyURL(resp.FinalURL, ""); final != "" {
		details.TaskLink = final
		details.Finished = details.Finished || isTaskEngineCompletedStudyURL(final)
	}
	details.StartTime, details.EndTime = extractTaskEngineDetailTimes(resp.Body)
	return nil
}

func extractTaskEngineUserID(resp learningResponse) string {
	if m := taskEngineUserIDRe.FindStringSubmatch(resp.Body); len(m) >= 2 {
		return m[1]
	}
	if u, err := url.Parse(resp.FinalURL); err == nil {
		return u.Query().Get("encryTaskUserId")
	}
	return ""
}

func extractTaskEngineDetailTimes(htmlContent string) (string, string) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "", ""
	}
	text := cleanText(textOf(doc))
	if text == "" {
		return "", ""
	}
	start, end := "", ""
	if m := taskEngineStartTimeRe.FindStringSubmatch(text); len(m) >= 2 {
		start = m[1]
	}
	if m := taskEngineEndTimeRe.FindStringSubmatch(text); len(m) >= 2 {
		end = m[1]
	}
	if m := taskEnginePeriodRe.FindStringSubmatch(text); len(m) >= 3 {
		if start == "" {
			start = m[1]
		}
		if end == "" {
			end = m[2]
		}
	}
	return normalizeTaskEngineDateText(start), normalizeTaskEngineDateText(end)
}

func taskEngineSummaryItem(pkg map[string]interface{}, course learningCourse, taskID string) LearningItem {
	planCount := int64FromAny(pkg["planCount"])
	planFinishCount := int64FromAny(pkg["planFinishCount"])
	studyProgress := int64FromAny(pkg["taskStudyProgress"])
	qualifyStatus := strVal(pkg["userTaskQualifyStatus"])
	finished := containsAny(qualifyStatus, "已达标", "已完成") ||
		studyProgress >= 1 ||
		(planCount > 0 && planFinishCount >= planCount)

	progressText := ""
	if planCount > 0 {
		progressText = fmt.Sprintf("%d/%d", planFinishCount, planCount)
	}
	status := "进行中"
	if finished {
		status = "已结束"
	}
	return LearningItem{
		ID:         "task-engine-" + firstNonEmpty(taskID, fmt.Sprintf("%d-%s", course.CourseID, strVal(pkg["name"]))),
		Kind:       learningKindActivity,
		Type:       taskEngineSummaryType,
		Title:      firstNonEmpty(strVal(pkg["name"]), "未命名任务引擎任务"),
		Status:     status,
		CourseName: course.CourseName,
		Info:       progressText,
		Link:       taskEngineJumpLink(taskID, course),
		CourseID:   course.CourseID,
		ClassID:    course.ClassID,
		Pending:    !finished,
		Finished:   finished,
		Ongoing:    !finished,
	}
}

func taskEngineNumericID(v interface{}) string {
	if id := int64FromAny(v); id > 0 {
		return strconv.FormatInt(id, 10)
	}
	return ""
}

func taskEnginePlanItem(plan map[string]interface{}, course learningCourse, taskID, fallbackTitle string, details taskEnginePlanDetails) LearningItem {
	planType := taskEnginePlanTypeName(plan)
	finished := taskEnginePlanIsFinished(plan) || details.Finished || isTaskEngineCompletedStudyURL(details.TaskLink)
	startTime := parseTaskEngineDate(firstNonEmpty(strVal(plan["startDateStr"]), details.StartTime))
	endTime := parseTaskEngineDate(firstNonEmpty(strVal(plan["endDateStr"]), details.EndTime))
	expired := !finished && endTime > 0 && endTime < time.Now().UnixMilli()
	planAllowStudy, hasAllowStudy := plan["planAllowStudy"]
	locked := hasAllowStudy && !boolFromAny(planAllowStudy)

	status := "进行中"
	if finished {
		status = "已完成"
	} else if expired {
		status = "已过期"
	} else if locked {
		status = "未开始"
	}

	kind := learningKindActivity
	switch {
	case planType == "作业" || planType == "AI实践":
		kind = learningKindHomework
	case planType == "考试":
		kind = learningKindExam
	}

	ongoing := !finished && !expired && !locked
	info := formatLearningTime(endTime)
	if info == "" {
		info = formatLearningTime(startTime)
	}
	return LearningItem{
		ID:         fmt.Sprintf("task-engine-%s-plan-%s", taskID, firstNonEmpty(taskEngineNumericID(plan["planId"]), strVal(plan["name"]))),
		Kind:       kind,
		Type:       planType,
		Title:      firstNonEmpty(strVal(plan["name"]), fallbackTitle, "未命名任务"),
		Status:     status,
		CourseName: course.CourseName,
		Info:       info,
		Link:       firstNonEmpty(details.TaskLink, taskEngineJumpLink(taskID, course)),
		CourseID:   course.CourseID,
		ClassID:    course.ClassID,
		StartTime:  startTime,
		EndTime:    endTime,
		Pending:    ongoing,
		Finished:   finished,
		Expired:    expired,
		Ongoing:    ongoing,
	}
}

func taskEngineJumpLink(taskID string, course learningCourse) string {
	if taskID == "" {
		return ""
	}
	return fmt.Sprintf("https://task.chaoxing.com/api/v1/middlePageApi/jumpStudyPlanList?taskId=%s&moocClassId=%s", url.QueryEscape(taskID), url.QueryEscape(strconv.FormatInt(course.ClassID, 10)))
}

func taskEnginePlanTypeName(plan map[string]interface{}) string {
	searchable := strings.ToLower(strings.TrimSpace(strVal(plan["planTypeName"])) + " " + strVal(plan["name"]))
	switch {
	case containsAny(searchable, "ai实践", "ai评价", "ai对话"):
		return "AI实践"
	case containsAny(searchable, "分组") && containsAny(searchable, "任务", "作业"):
		return "分组任务"
	case containsAny(searchable, "讨论"):
		return "分组讨论"
	case containsAny(searchable, "考试"):
		return "考试"
	case containsAny(searchable, "作业"):
		return "作业"
	case containsAny(searchable, "随堂练习"):
		return "随堂练习"
	case containsAny(searchable, "测验", "自测", "练习"):
		return "测验"
	case containsAny(searchable, "签到"):
		return "签到"
	case containsAny(searchable, "问卷"):
		return "问卷"
	case containsAny(searchable, "抢答"):
		return "抢答"
	case containsAny(searchable, "投票"):
		return "投票"
	case containsAny(searchable, "直播"):
		return "直播"
	case containsAny(searchable, "评分"):
		return "评分"
	case containsAny(searchable, "拍照"):
		return "拍照"
	case containsAny(searchable, "笔记"):
		return "笔记"
	case containsAny(searchable, "课程", "章节", "知识点", "视频", "文档", "音频", "阅读"):
		return "视频/任务点"
	}
	if name, ok := taskEnginePlanTypeNames[int64FromAny(plan["planType"])]; ok {
		return name
	}
	return "其他任务"
}

func taskEnginePlanIsFinished(plan map[string]interface{}) bool {
	if b, ok := plan["isFinish"].(bool); ok && b {
		return true
	}
	if int64FromAny(plan["isFinish"]) == 1 {
		return true
	}
	if planUser, ok := plan["planUser"].(map[string]interface{}); ok {
		if int64FromAny(planUser["finish"]) == 1 {
			return true
		}
	}
	if score, ok := plan["score"].(map[string]interface{}); ok {
		if int64FromAny(score["completed"]) == 1 {
			return true
		}
	}
	return false
}

func normalizeTaskEngineStudyURL(rawURL, domainName string) string {
	if rawURL == "" {
		return ""
	}
	base := "https://task.chaoxing.com/"
	if domainName != "" {
		if u, err := url.Parse(domainName); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
			base = domainName
		}
	}
	ref, err := url.Parse(base)
	if err != nil {
		return ""
	}
	resolved, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	abs := ref.ResolveReference(resolved)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	return abs.String()
}

func isTaskEngineCompletedStudyURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host != "chaoxing.com" && !strings.HasSuffix(host, ".chaoxing.com") {
		return false
	}
	return strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/exam/test/look")
}

func normalizeTaskEngineDateText(value string) string {
	replacer := strings.NewReplacer("年", "-", "/", "-", "月", "-", "日", "", ".", "-")
	return strings.Join(strings.Fields(replacer.Replace(strings.TrimSpace(value))), " ")
}

// parseTaskEngineDate 解析任务引擎的日期文本，返回毫秒时间戳，无法解析时返回 0。
// 平台详情页常返回 "09-20 20:00" 这种省略年份的日期，需要按当前年补齐。
func parseTaskEngineDate(value string) int64 {
	text := normalizeTaskEngineDateText(value)
	if text == "" {
		return 0
	}
	if m := taskEngineShortDateRe.FindStringSubmatch(text); m != nil {
		month, _ := strconv.Atoi(m[1])
		day, _ := strconv.Atoi(m[2])
		hour, minute, second := 0, 0, 0
		if m[3] != "" {
			hour, _ = strconv.Atoi(m[3])
			minute, _ = strconv.Atoi(m[4])
		}
		if m[5] != "" {
			second, _ = strconv.Atoi(m[5])
		}
		now := time.Now()
		parsed := time.Date(now.Year(), time.Month(month), day, hour, minute, second, 0, now.Location())
		if parsed.Month() == time.Month(month) && parsed.Day() == day {
			return parsed.UnixMilli()
		}
		return 0
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
		"2006-1-2 15:04:05", "2006-1-2 15:04", "2006-1-2",
		"01-02 15:04:05", "01-02 15:04", "1-2 15:04:05", "1-2 15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}
