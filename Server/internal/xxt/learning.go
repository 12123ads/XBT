package xxt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	learningKindHomework = "homework"
	learningKindExam     = "exam"
	learningKindActivity = "activity"
	learningKindProgress = "progress"
)

const (
	learningProgressMaxConcurrent = 6
	learningProgressCourseTimeout = 2 * time.Second
	learningProgressTotalTimeout  = 7 * time.Second
)

type LearningDashboard struct {
	Todo       []LearningItem `json:"todo"`
	Homework   []LearningItem `json:"homework"`
	Exams      []LearningItem `json:"exams"`
	Activities []LearningItem `json:"activities"`
	Progress   []LearningItem `json:"progress"`
	Errors     []string       `json:"errors"`
}

type LearningItem struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Type       string `json:"type"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	CourseName string `json:"course_name"`
	Info       string `json:"info"`
	Link       string `json:"link"`
	Raw        string `json:"raw"`
	CourseID   int64  `json:"course_id"`
	ClassID    int64  `json:"class_id"`
	ActiveID   int64  `json:"active_id"`
	StartTime  int64  `json:"start_time"`
	EndTime    int64  `json:"end_time"`
	Pending    bool   `json:"pending"`
	Finished   bool   `json:"finished"`
	Expired    bool   `json:"expired"`
	Ongoing    bool   `json:"ongoing"`

	CompletedTasks int    `json:"completed_tasks"`
	TotalTasks     int    `json:"total_tasks"`
	CompletionRate string `json:"completion_rate"`
	CourseScore    string `json:"course_score"`
	ChapterQuiz    string `json:"chapter_quiz"`
	Ranking        string `json:"ranking"`
	AIPractice     string `json:"ai_practice"`
	GroupTask      string `json:"group_task"`
}

type LearningCourseRef struct {
	CourseID   int64  `gorm:"column:course_id"`
	ClassID    int64  `gorm:"column:class_id"`
	CourseName string `gorm:"column:course_name"`
	Teacher    string `gorm:"column:teacher"`
}

type learningCourse struct {
	CourseID   int64
	ClassID    int64
	CourseName string
	Teacher    string
	CPI        string
	Enc        string
	PEnc       string
}

func (c *Client) GetLearningDashboard(mobile, password string, progressCourses []LearningCourseRef) (LearningDashboard, error) {
	s, err := c.ensureSession(mobile, password)
	if err != nil {
		return LearningDashboard{}, err
	}
	cli := *c.http
	cli.Jar = s.Jar

	var out LearningDashboard
	var homework []LearningItem
	var exams []LearningItem
	var activities []LearningItem
	var progress []LearningItem
	var errorsMu sync.Mutex
	addErr := func(section string, err error) {
		if err == nil {
			return
		}
		errorsMu.Lock()
		out.Errors = append(out.Errors, section+": "+err.Error())
		errorsMu.Unlock()
	}

	var wg sync.WaitGroup
	wg.Add(4)
	go func() {
		defer wg.Done()
		items, err := c.GetLearningHomework(&cli)
		if err != nil {
			addErr("homework", err)
			return
		}
		homework = items
	}()
	go func() {
		defer wg.Done()
		items, err := c.GetLearningExams(&cli)
		if err != nil {
			addErr("exams", err)
			return
		}
		exams = items
	}()
	go func() {
		defer wg.Done()
		items, err := c.GetLearningActivities(&cli)
		if err != nil {
			addErr("activities", err)
			return
		}
		activities = items
	}()
	go func() {
		defer wg.Done()
		items, err := c.GetLearningProgress(&cli, progressCourses)
		if err != nil {
			addErr("progress", err)
			return
		}
		progress = items
	}()
	wg.Wait()

	out.Homework = sortLearningItems(homework)
	out.Exams = sortLearningItems(exams)
	out.Activities = sortLearningItems(activities)
	out.Progress = sortLearningItems(progress)
	out.Todo = buildLearningTodo(out.Homework, out.Exams, out.Activities)
	return out, nil
}

func (c *Client) GetLearningHomework(cli *http.Client) ([]LearningItem, error) {
	doc, err := c.getHTML(cli, "https://mooc1-api.chaoxing.com/work/stu-work", "Mozilla/5.0")
	if err != nil {
		return nil, err
	}
	items := make([]LearningItem, 0)
	for _, li := range findNodes(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "li" && parentMatches(n, "ul", "nav")
	}) {
		option := firstNode(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "div" && attr(n, "role") == "option"
		})
		if option == nil {
			continue
		}
		title := cleanText(textOf(firstNode(option, tagEquals("p"))))
		spans := findNodes(option, tagEquals("span"))
		status, courseName := "", ""
		pending := false
		if len(spans) > 0 {
			status = cleanText(textOf(spans[0]))
			pending = strings.Contains(attr(spans[0], "class"), "status")
		}
		if len(spans) > 1 {
			courseName = cleanText(textOf(spans[1]))
		}
		info := cleanText(textOf(firstNode(option, func(n *html.Node) bool {
			return n.Type == html.ElementNode && hasClass(n, "fr")
		})))
		raw := attr(li, "data")
		courseID, classID, workID := extractLearningIDs(raw)
		id := firstNonEmpty(workID, fmt.Sprintf("%d:%d:%s", courseID, classID, title))
		items = append(items, LearningItem{
			ID:         id,
			Kind:       learningKindHomework,
			Type:       "作业",
			Title:      title,
			Status:     status,
			CourseName: courseName,
			Info:       info,
			Link:       learningCourseLink(courseID, classID, "8"),
			Raw:        raw,
			CourseID:   courseID,
			ClassID:    classID,
			Pending:    pending,
			Finished:   !pending && strings.Contains(status, "已"),
		})
	}
	return compactLearningItems(items), nil
}

func (c *Client) GetLearningExams(cli *http.Client) ([]LearningItem, error) {
	var all []LearningItem
	var errs []string

	doc, err := c.getHTML(cli, "https://mooc1-api.chaoxing.com/exam-ans/exam/phone/examcode", "Mozilla/5.0")
	if err == nil {
		all = append(all, extractPhoneExamItems(doc)...)
	} else {
		errs = append(errs, err.Error())
	}

	doc2, err := c.getHTML(cli, "https://mooc1.chaoxing.com/exam-ans/exam/test/examcode/examlist?edition=1&nohead=0&fid=", "Mozilla/5.0")
	if err == nil {
		all = append(all, extractTableExamItems(doc2)...)
	} else {
		errs = append(errs, err.Error())
	}

	all = dedupeLearningItems(all)
	if len(all) > 0 || len(errs) == 0 {
		return all, nil
	}
	return nil, fmt.Errorf(strings.Join(errs, "; "))
}

func extractPhoneExamItems(doc *html.Node) []LearningItem {
	items := make([]LearningItem, 0)
	for _, li := range findNodes(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "li" && parentMatches(n, "ul", "ks_list")
	}) {
		dl := firstNode(li, tagEquals("dl"))
		title, info := "", ""
		if dl != nil {
			title = cleanText(textOf(firstNode(dl, tagEquals("dt"))))
			info = cleanText(textOf(firstNode(dl, tagEquals("dd"))))
		}
		img := firstNode(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "img"
		})
		expired := img != nil && strings.Contains(attr(img, "src"), "ks_02")
		status := cleanText(textOf(firstNode(li, func(n *html.Node) bool {
			return n.Type == html.ElementNode && n.Data == "span" && hasClass(n, "ks_state")
		})))
		raw := attr(li, "data")
		courseID, classID, examID := extractLearningIDs(raw)
		finished := containsAny(status, "已完成", "待批阅")
		items = append(items, LearningItem{
			ID:       firstNonEmpty(examID, fmt.Sprintf("%d:%d:%s", courseID, classID, title)),
			Kind:     learningKindExam,
			Type:     "考试",
			Title:    title,
			Status:   status,
			Info:     info,
			Link:     learningExamLink(courseID, classID, examID),
			Raw:      raw,
			CourseID: courseID,
			ClassID:  classID,
			Pending:  !finished && !expired,
			Finished: finished,
			Expired:  expired,
		})
	}
	return compactLearningItems(items)
}

func extractTableExamItems(doc *html.Node) []LearningItem {
	items := make([]LearningItem, 0)
	for _, tr := range findNodes(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "tr" && hasClass(n, "dataTr")
	}) {
		cells := directChildren(tr, "td")
		if len(cells) < 9 {
			continue
		}
		title := cleanText(textOf(cells[1]))
		timeRange := cleanText(textOf(cells[2]))
		examStatus := cleanText(textOf(cells[4]))
		answerStatus := cleanText(textOf(cells[5]))
		status := firstNonEmpty(answerStatus, examStatus)
		action := firstNode(cells[8], tagEquals("a"))
		raw := ""
		if action != nil {
			raw = extractGoURL(attr(action, "onclick"))
		}
		courseID, classID, examID := extractExamTableIDs(raw, attr(action, "onclick"))
		expired := strings.Contains(examStatus, "已结束")
		finished := containsAny(answerStatus, "已完成", "待批阅")
		info := timeRange
		if expired {
			info = "已结束"
		}
		items = append(items, LearningItem{
			ID:       firstNonEmpty(examID, fmt.Sprintf("%d:%d:%s", courseID, classID, title)),
			Kind:     learningKindExam,
			Type:     "考试",
			Title:    title,
			Status:   status,
			Info:     info,
			Link:     learningExamLink(courseID, classID, examID),
			Raw:      raw,
			CourseID: courseID,
			ClassID:  classID,
			Pending:  !finished && !expired,
			Finished: finished,
			Expired:  expired,
		})
	}
	return compactLearningItems(items)
}

func (c *Client) GetLearningActivities(cli *http.Client) ([]LearningItem, error) {
	courses, err := c.getLearningCourses(cli)
	if err != nil {
		return nil, err
	}
	all := make([]LearningItem, 0)
	for _, course := range courses {
		items, err := c.getLearningActivitiesForCourse(cli, course)
		if err != nil {
			continue
		}
		all = append(all, items...)
	}
	return sortLearningItems(all), nil
}

func (c *Client) GetLearningProgress(cli *http.Client, selected []LearningCourseRef) ([]LearningItem, error) {
	if len(selected) == 0 {
		return []LearningItem{}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), learningProgressTotalTimeout)
	defer cancel()

	courses, err := c.getLearningCoursesWithContext(ctx, cli)
	if err != nil {
		return nil, err
	}
	courses = filterLearningProgressCourses(courses, selected)
	items := make([]LearningItem, len(courses))
	for i, course := range courses {
		items[i] = defaultLearningProgressItem(course)
	}

	sem := make(chan struct{}, learningProgressMaxConcurrent)
	var wg sync.WaitGroup
	for i, course := range courses {
		i, course := i, course
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			courseCtx, courseCancel := context.WithTimeout(ctx, learningProgressCourseTimeout)
			defer courseCancel()
			items[i] = c.getLearningProgressForCourse(courseCtx, cli, course)
		}()
	}
	wg.Wait()
	return sortLearningItems(items), nil
}

func filterLearningProgressCourses(courses []learningCourse, selected []LearningCourseRef) []learningCourse {
	selectedByKey := make(map[string]LearningCourseRef, len(selected))
	order := make([]string, 0, len(selected))
	for _, ref := range selected {
		if ref.CourseID == 0 || ref.ClassID == 0 {
			continue
		}
		key := learningCourseKey(ref.CourseID, ref.ClassID)
		if _, exists := selectedByKey[key]; exists {
			continue
		}
		selectedByKey[key] = ref
		order = append(order, key)
	}
	if len(selectedByKey) == 0 {
		return nil
	}

	remoteByKey := make(map[string]learningCourse, len(courses))
	for _, course := range courses {
		key := learningCourseKey(course.CourseID, course.ClassID)
		ref, ok := selectedByKey[key]
		if !ok {
			continue
		}
		if course.CourseName == "" {
			course.CourseName = ref.CourseName
		}
		if course.Teacher == "" {
			course.Teacher = ref.Teacher
		}
		remoteByKey[key] = course
	}

	out := make([]learningCourse, 0, len(order))
	for _, key := range order {
		if course, ok := remoteByKey[key]; ok {
			out = append(out, course)
			continue
		}
		ref := selectedByKey[key]
		out = append(out, learningCourse{
			CourseID:   ref.CourseID,
			ClassID:    ref.ClassID,
			CourseName: ref.CourseName,
			Teacher:    ref.Teacher,
		})
	}
	return out
}

func learningCourseKey(courseID, classID int64) string {
	return fmt.Sprintf("%d_%d", courseID, classID)
}

func defaultLearningProgressItem(course learningCourse) LearningItem {
	return LearningItem{
		ID:             fmt.Sprintf("%d:%d", course.CourseID, course.ClassID),
		Kind:           learningKindProgress,
		Type:           "课程进度",
		Title:          firstNonEmpty(course.CourseName, "未知课程"),
		Status:         "未知",
		CourseName:     course.CourseName,
		Info:           "点击查看",
		Link:           learningStudyDataLink(course),
		CourseID:       course.CourseID,
		ClassID:        course.ClassID,
		CompletionRate: "点击查看",
		CourseScore:    "--",
		ChapterQuiz:    "--",
		Ranking:        "--",
		AIPractice:     "--",
		GroupTask:      "--",
	}
}

func (c *Client) getLearningProgressForCourse(ctx context.Context, cli *http.Client, course learningCourse) LearningItem {
	pEnc := firstNonEmpty(course.PEnc, course.Enc)
	if pEnc == "" && course.ClassID > 0 {
		pEnc = "stuenc_" + strconv.FormatInt(course.ClassID, 10)
	}
	link := learningStudyDataLink(course)
	item := LearningItem{
		ID:             fmt.Sprintf("%d:%d", course.CourseID, course.ClassID),
		Kind:           learningKindProgress,
		Type:           "课程进度",
		Title:          firstNonEmpty(course.CourseName, "未知课程"),
		Status:         "未知",
		CourseName:     course.CourseName,
		Info:           "点击查看",
		Link:           link,
		CourseID:       course.CourseID,
		ClassID:        course.ClassID,
		CompletionRate: "点击查看",
		CourseScore:    "--",
		ChapterQuiz:    "--",
		Ranking:        "--",
		AIPractice:     "--",
		GroupTask:      "--",
	}
	if course.CourseID == 0 || course.ClassID == 0 || course.CPI == "" {
		return item
	}

	base := url.Values{}
	base.Set("clazzid", strconv.FormatInt(course.ClassID, 10))
	base.Set("courseid", strconv.FormatInt(course.CourseID, 10))
	base.Set("cpi", course.CPI)
	base.Set("ut", "s")
	base.Set("pEnc", pEnc)
	baseParams := base.Encode()
	referer := learningStudyDataLink(course)

	jobURL := "https://stat2-ans.chaoxing.com/stat2/study-data/job?" + baseParams
	if payload, err := c.getLearningJSON(ctx, cli, http.MethodGet, jobURL, nil, referer); err == nil {
		data := mapFromAny(deepFindFirst(payload, "data"))
		completed := int(int64FromAny(data["job"]))
		total := int(int64FromAny(firstNonNil(data["publishJobNum"], data["publishJobnum"])))
		if total == 0 {
			total = completed
		}
		item.CompletedTasks = completed
		item.TotalTasks = total
		if per := strVal(data["jobPer"]); per != "" {
			item.CompletionRate = strings.TrimSuffix(per, "%") + "%"
		} else if total > 0 {
			item.CompletionRate = fmt.Sprintf("%d%%", completed*100/total)
		}
		item.Info = item.CompletionRate
		item.ChapterQuiz = formatLearningPair(data["job"], firstNonNil(data["publishJobNum"], data["publishJobnum"]))
		if rank := int64FromAny(data["jobRank"]); rank > 0 {
			item.Ranking = fmt.Sprintf("第%d名", rank)
		}
		item.AIPractice = formatLearningPair(data["aiEvaluateReachCount"], data["publishLibraryNum"])
		item.GroupTask = formatLearningPair(data["taskNum"], data["publishTaskNum"])
	}

	pointURL := "https://stat2-ans.chaoxing.com/stat2/study-data/point"
	if payload, err := c.getLearningJSON(ctx, cli, http.MethodPost, pointURL, []byte(baseParams), referer); err == nil {
		if points := strVal(firstNonNil(deepFindFirst(payload, "ponits"), deepFindFirst(payload, "points"))); points != "" {
			item.CourseScore = points + "分"
		}
	}

	aiURL := "https://stat2-ans.chaoxing.com/stat2/study-data/ai-evaluate-stat?" + baseParams
	if payload, err := c.getLearningJSON(ctx, cli, http.MethodGet, aiURL, nil, referer); err == nil {
		data := mapFromAny(deepFindFirst(payload, "data"))
		if pair := formatLearningPair(data["aiEvaluateReachCount"], data["publishLibraryNum"]); pair != "--" {
			item.AIPractice = pair
		}
	}

	groupURL := "https://stat2-ans.chaoxing.com/stat2/study-data/groupTask?" + baseParams
	if payload, err := c.getLearningJSON(ctx, cli, http.MethodGet, groupURL, nil, referer); err == nil {
		data := mapFromAny(deepFindFirst(payload, "data"))
		if pair := formatLearningPair(data["taskNum"], data["publishTaskNum"]); pair != "--" {
			item.GroupTask = pair
		}
	}

	if item.TotalTasks > 0 && item.CompletedTasks >= item.TotalTasks {
		item.Status = "已完成"
		item.Finished = true
		item.Pending = false
	} else if item.TotalTasks > 0 {
		item.Status = "进行中"
		item.Pending = true
		item.Finished = false
	} else {
		item.Pending = false
	}
	return item
}

func (c *Client) getLearningJSON(ctx context.Context, cli *http.Client, method, rawURL string, body []byte, referer string) (interface{}, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (c *Client) getLearningActivitiesForCourse(cli *http.Client, course learningCourse) ([]LearningItem, error) {
	u := fmt.Sprintf("https://mobilelearn.chaoxing.com/v2/apis/active/student/activelist?fid=0&courseId=%d&classId=%d&showNotStartedActive=0&_=%d", course.CourseID, course.ClassID, time.Now().UnixMilli())
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("User-Agent", c.mobileUA)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	activeList := findActiveList(payload)
	if len(activeList) == 0 {
		activeList = findBestActivityArray(payload)
	}
	items := make([]LearningItem, 0, len(activeList))
	for _, item := range activeList {
		activeType := int(int64FromAny(firstNonNil(item["activeType"], item["type"], item["atype"])))
		activeID := int64FromAny(firstNonNil(item["id"], item["activeId"], item["active_id"]))
		startTime := parseTimeMillis(firstNonNil(item["startTime"], item["start_time"], item["beginTime"]))
		endTime := parseTimeMillis(firstNonNil(item["endTime"], item["end_time"], item["deadline"]))
		statusCode := int64FromAny(item["status"])
		ongoing := statusCode == 1
		ended := statusCode == 2
		status := "未开始"
		if ongoing {
			status = "进行中"
		} else if ended {
			status = "已结束"
		}
		items = append(items, LearningItem{
			ID:         strconv.FormatInt(activeID, 10),
			Kind:       learningKindActivity,
			Type:       learningActivityTypeName(activeType),
			Title:      firstNonEmpty(strVal(firstNonNil(item["nameOne"], item["name"], item["activeName"], item["title"])), "未知任务"),
			Status:     status,
			CourseName: course.CourseName,
			Info:       formatLearningTime(endTime),
			Link:       learningCourseLink(course.CourseID, course.ClassID, "0"),
			CourseID:   course.CourseID,
			ClassID:    course.ClassID,
			ActiveID:   activeID,
			StartTime:  startTime,
			EndTime:    endTime,
			Pending:    ongoing,
			Expired:    ended,
			Ongoing:    ongoing,
		})
	}
	return compactLearningItems(items), nil
}

func (c *Client) getLearningCourses(cli *http.Client) ([]learningCourse, error) {
	return c.getLearningCoursesWithContext(context.Background(), cli)
}

func (c *Client) getLearningCoursesWithContext(ctx context.Context, cli *http.Client) ([]learningCourse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://mooc1-api.chaoxing.com/mycourse/backclazzdata?view=json&mcode=", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var payload struct {
		ChannelList []struct {
			Key     interface{}            `json:"key"`
			CPI     interface{}            `json:"cpi"`
			CataID  string                 `json:"cataid"`
			Content map[string]interface{} `json:"content"`
		} `json:"channelList"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	courses := make([]learningCourse, 0)
	for _, ch := range payload.ChannelList {
		content := ch.Content
		if content == nil {
			continue
		}
		if _, ok := content["folderName"]; ok {
			continue
		}
		if rt, ok := content["roletype"].(float64); ok && int(rt) == 1 {
			continue
		}
		courseMap, ok := content["course"].(map[string]interface{})
		if !ok {
			continue
		}
		dataArr, ok := courseMap["data"].([]interface{})
		if !ok {
			continue
		}
		classID := int64FromAny(content["id"])
		if classID == 0 {
			classID = int64FromAny(ch.Key)
		}
		if clazz, ok := content["clazz"].(map[string]interface{}); ok {
			if arr, ok := clazz["data"].([]interface{}); ok && len(arr) > 0 {
				if m, ok := arr[0].(map[string]interface{}); ok {
					classID = int64FromAny(firstNonNil(m["id"], m["clazzId"], m["classId"]))
				}
			}
		}
		for _, item := range dataArr {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			courseID := int64FromAny(m["id"])
			if courseID == 0 {
				squareURL, _ := m["courseSquareUrl"].(string)
				u2, err := url.Parse(squareURL)
				if err == nil {
					courseID, _ = strconv.ParseInt(u2.Query().Get("courseId"), 10, 64)
					if classID == 0 {
						classID, _ = strconv.ParseInt(u2.Query().Get("classId"), 10, 64)
					}
				}
			}
			if courseID == 0 || classID == 0 {
				continue
			}
			key := fmt.Sprintf("%d_%d", courseID, classID)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			courses = append(courses, learningCourse{
				CourseID:   courseID,
				ClassID:    classID,
				CourseName: strVal(m["name"]),
				Teacher:    strVal(m["teacherfactor"]),
				CPI:        strVal(firstNonNil(content["cpi"], ch.CPI)),
				Enc:        strVal(firstNonNil(content["enc"], m["enc"])),
				PEnc:       strings.Trim(strVal(firstNonNil(content["pEnc"], m["pEnc"])), `"'`),
			})
		}
	}
	return courses, nil
}

func (c *Client) getHTML(cli *http.Client, rawURL, ua string) (*html.Node, error) {
	req, _ := http.NewRequest(http.MethodGet, rawURL, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return html.Parse(bytes.NewReader(body))
}

func buildLearningTodo(homework, exams, activities []LearningItem) []LearningItem {
	out := make([]LearningItem, 0)
	for _, item := range homework {
		if item.Pending {
			out = append(out, item)
		}
	}
	for _, item := range exams {
		if item.Pending {
			out = append(out, item)
		}
	}
	for _, item := range activities {
		if item.Ongoing {
			out = append(out, item)
		}
	}
	return sortLearningItems(out)
}

func sortLearningItems(items []LearningItem) []LearningItem {
	sort.SliceStable(items, func(i, j int) bool {
		leftRank := learningItemStatusRank(items[i])
		rightRank := learningItemStatusRank(items[j])
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftTime := learningItemSortTime(items[i])
		rightTime := learningItemSortTime(items[j])
		if leftTime != rightTime {
			return leftTime < rightTime
		}
		if items[i].CourseName != items[j].CourseName {
			return items[i].CourseName < items[j].CourseName
		}
		return items[i].Title < items[j].Title
	})
	return items
}

func learningItemStatusRank(item LearningItem) int {
	if (item.Ongoing || item.Pending) && !item.Finished && !item.Expired {
		return 0
	}
	if item.Expired || strings.Contains(item.Status, "已结束") {
		return 1
	}
	if item.Finished {
		return 2
	}
	return 3
}

func learningItemSortTime(item LearningItem) int64 {
	if item.EndTime > 0 {
		return item.EndTime
	}
	if item.StartTime > 0 {
		return item.StartTime
	}
	return int64(^uint64(0) >> 1)
}

func learningStudyDataLink(course learningCourse) string {
	if course.CourseID == 0 || course.ClassID == 0 {
		return ""
	}
	q := url.Values{}
	q.Set("courseid", strconv.FormatInt(course.CourseID, 10))
	q.Set("clazzid", strconv.FormatInt(course.ClassID, 10))
	if course.CPI != "" {
		q.Set("cpi", course.CPI)
	}
	q.Set("ut", "s")
	return "https://stat2-ans.chaoxing.com/study-data/index?" + q.Encode()
}

func formatLearningPair(doneRaw, totalRaw interface{}) string {
	done := int64FromAny(doneRaw)
	total := int64FromAny(totalRaw)
	if doneRaw == nil || totalRaw == nil {
		return "--"
	}
	return fmt.Sprintf("%d/%d", done, total)
}

func mapFromAny(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func extractLearningIDs(raw string) (courseID, classID int64, taskID string) {
	u, err := url.Parse(raw)
	if err != nil {
		u, err = url.Parse("https://mooc1-api.chaoxing.com" + raw)
	}
	if err != nil {
		return 0, 0, ""
	}
	q := u.Query()
	courseID, _ = strconv.ParseInt(firstNonEmpty(q.Get("courseId"), q.Get("courseid"), q.Get("moocId")), 10, 64)
	classID, _ = strconv.ParseInt(firstNonEmpty(q.Get("clazzId"), q.Get("classId"), q.Get("clazzid")), 10, 64)
	taskID = firstNonEmpty(q.Get("taskrefId"), q.Get("examId"), q.Get("workId"))
	return courseID, classID, taskID
}

func extractExamTableIDs(raw, onclick string) (courseID, classID int64, examID string) {
	if raw != "" {
		u, err := url.Parse(raw)
		if err == nil {
			refer := u.Query().Get("refer")
			if refer != "" {
				if decoded, err := url.QueryUnescape(refer); err == nil {
					if ru, err := url.Parse(decoded); err == nil {
						q := ru.Query()
						courseID, _ = strconv.ParseInt(firstNonEmpty(q.Get("courseId"), q.Get("courseid")), 10, 64)
						classID, _ = strconv.ParseInt(firstNonEmpty(q.Get("classId"), q.Get("clazzId"), q.Get("clazzid")), 10, 64)
						examID = q.Get("examId")
					}
				}
			}
			q := u.Query()
			if courseID == 0 {
				courseID, _ = strconv.ParseInt(firstNonEmpty(q.Get("courseId"), q.Get("moocId")), 10, 64)
			}
			if classID == 0 {
				classID, _ = strconv.ParseInt(firstNonEmpty(q.Get("classId"), q.Get("clazzid")), 10, 64)
			}
			if examID == "" {
				examID = q.Get("examId")
			}
		}
	}
	if courseID == 0 {
		courseID = regexpInt(onclick, `moocId=(\d+)`)
	}
	if classID == 0 {
		classID = regexpInt(onclick, `clazzid=(\d+)`)
	}
	if examID == "" {
		examID = regexpString(onclick, `examId=(\d+)`)
	}
	return courseID, classID, examID
}

func learningCourseLink(courseID, classID int64, pageHeader string) string {
	if courseID == 0 || classID == 0 {
		return ""
	}
	q := url.Values{}
	q.Set("ismooc2", "1")
	q.Set("courseid", strconv.FormatInt(courseID, 10))
	q.Set("clazzid", strconv.FormatInt(classID, 10))
	if pageHeader != "" {
		q.Set("pageHeader", pageHeader)
	}
	return "https://mooc1.chaoxing.com/visit/stucoursemiddle?" + q.Encode()
}

func learningExamLink(courseID, classID int64, examID string) string {
	if courseID == 0 || classID == 0 || examID == "" {
		return ""
	}
	q := url.Values{}
	q.Set("courseId", strconv.FormatInt(courseID, 10))
	q.Set("classId", strconv.FormatInt(classID, 10))
	q.Set("examId", examID)
	return "https://mooc1-api.chaoxing.com/exam-ans/exam/test/examcode/examnotes?" + q.Encode()
}

func learningActivityTypeName(t int) string {
	switch t {
	case 0, 2:
		return "签到"
	case 4:
		return "抢答"
	case 5:
		return "主题讨论"
	case 6:
		return "投票"
	case 14:
		return "问卷"
	case 17:
		return "直播"
	case 23, 42:
		return "随堂练习"
	case 35:
		return "分组任务"
	case 43:
		return "评分"
	case 45:
		return "拍照"
	case 47:
		return "作业"
	case 64:
		return "笔记"
	default:
		if t > 0 {
			return fmt.Sprintf("类型%d", t)
		}
		return "课程任务"
	}
}

func formatLearningTime(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("2006-01-02 15:04")
}

func compactLearningItems(items []LearningItem) []LearningItem {
	out := items[:0]
	for _, item := range items {
		item.Title = cleanText(item.Title)
		item.Status = cleanText(item.Status)
		item.CourseName = cleanText(item.CourseName)
		item.Info = cleanText(item.Info)
		if item.Title == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func dedupeLearningItems(items []LearningItem) []LearningItem {
	seen := make(map[string]struct{})
	out := make([]LearningItem, 0, len(items))
	for _, item := range items {
		key := item.Kind + ":" + firstNonEmpty(item.ID, item.Title)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func cleanText(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func findNodes(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	out := make([]*html.Node, 0)
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil {
			return
		}
		if pred(cur) {
			out = append(out, cur)
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return out
}

func firstNode(n *html.Node, pred func(*html.Node) bool) *html.Node {
	var found *html.Node
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur == nil || found != nil {
			return
		}
		if pred(cur) {
			found = cur
			return
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return found
}

func tagEquals(tag string) func(*html.Node) bool {
	return func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == tag
	}
}

func parentMatches(n *html.Node, tag, className string) bool {
	if n == nil || n.Parent == nil || n.Parent.Type != html.ElementNode || n.Parent.Data != tag {
		return false
	}
	return className == "" || hasClass(n.Parent, className)
}

func directChildren(n *html.Node, tag string) []*html.Node {
	out := make([]*html.Node, 0)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.ElementNode && child.Data == tag {
			out = append(out, child)
		}
	}
	return out
}

func attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, className string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == className {
			return true
		}
	}
	return false
}

func textOf(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
			b.WriteByte(' ')
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func containsAny(s string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func extractGoURL(onclick string) string {
	m := regexp.MustCompile(`go\(['"]([^'"]+)['"]\)`).FindStringSubmatch(onclick)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func regexpString(s, pattern string) string {
	m := regexp.MustCompile(pattern).FindStringSubmatch(s)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func regexpInt(s, pattern string) int64 {
	v, _ := strconv.ParseInt(regexpString(s, pattern), 10, 64)
	return v
}
