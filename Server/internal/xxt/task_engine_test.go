package xxt

import (
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func at(y int, m time.Month, d, hh, mm, ss int) int64 {
	return time.Date(y, m, d, hh, mm, ss, 0, time.Local).UnixMilli()
}

func TestParseTaskEngineDate(t *testing.T) {
	year := time.Now().Year()
	cases := []struct {
		name  string
		value string
		check func(int64) bool
	}{
		{"empty", "", func(v int64) bool { return v == 0 }},
		{"full datetime", "2026-03-15 09:30", func(v int64) bool { return v == at(2026, time.March, 15, 9, 30, 0) }},
		{"full datetime seconds", "2026-03-15 09:30:05", func(v int64) bool { return v == at(2026, time.March, 15, 9, 30, 5) }},
		{"chinese text", "2026年3月15日 09:30", func(v int64) bool { return v == at(2026, time.March, 15, 9, 30, 0) }},
		{"dotted", "2026.3.15 09:30", func(v int64) bool { return v == at(2026, time.March, 15, 9, 30, 0) }},
		{"short no year", "09-20 20:00", func(v int64) bool { return v == at(year, time.September, 20, 20, 0, 0) }},
		{"short no year no time", "09-20", func(v int64) bool { return v == at(year, time.September, 20, 0, 0, 0) }},
		{"invalid month", "13-20 20:00", func(v int64) bool { return v == 0 }},
		{"garbage", "not a date", func(v int64) bool { return v == 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseTaskEngineDate(tc.value); !tc.check(got) {
				t.Fatalf("parseTaskEngineDate(%q) = %d", tc.value, got)
			}
		})
	}
}

func TestTaskEnginePlanTypeName(t *testing.T) {
	cases := []struct {
		plan     map[string]interface{}
		expected string
	}{
		{map[string]interface{}{"planType": float64(4)}, "作业"},
		{map[string]interface{}{"planType": float64(5)}, "考试"},
		{map[string]interface{}{"planType": float64(1)}, "视频/任务点"},
		{map[string]interface{}{"planType": float64(9)}, "签到"},
		{map[string]interface{}{"planType": float64(99)}, "其他任务"},
		{map[string]interface{}{"planType": float64(1), "planTypeName": "章节学习"}, "视频/任务点"},
		{map[string]interface{}{"planType": float64(1), "name": "期末考试"}, "考试"},
		{map[string]interface{}{"planType": float64(1), "name": "AI对话练习"}, "AI实践"},
		{map[string]interface{}{"planType": float64(1), "name": "分组作业"}, "分组任务"},
		{map[string]interface{}{"planType": float64(1), "planTypeName": "课堂测验"}, "测验"},
	}
	for _, tc := range cases {
		if got := taskEnginePlanTypeName(tc.plan); got != tc.expected {
			t.Fatalf("taskEnginePlanTypeName(%v) = %q, want %q", tc.plan, got, tc.expected)
		}
	}
}

func TestTaskEnginePlanIsFinished(t *testing.T) {
	cases := []struct {
		plan     map[string]interface{}
		expected bool
	}{
		{map[string]interface{}{"isFinish": true}, true},
		{map[string]interface{}{"isFinish": float64(1)}, true},
		{map[string]interface{}{"isFinish": float64(0)}, false},
		{map[string]interface{}{"planUser": map[string]interface{}{"finish": float64(1)}}, true},
		{map[string]interface{}{"score": map[string]interface{}{"completed": float64(1)}}, true},
		{map[string]interface{}{}, false},
	}
	for _, tc := range cases {
		if got := taskEnginePlanIsFinished(tc.plan); got != tc.expected {
			t.Fatalf("taskEnginePlanIsFinished(%v) = %v, want %v", tc.plan, got, tc.expected)
		}
	}
}

func TestIsTaskEngineCompletedStudyURL(t *testing.T) {
	cases := []struct {
		rawURL   string
		expected bool
	}{
		{"https://mooc1-api.chaoxing.com/exam-ans/exam/test/look", true},
		{"https://mooc1-api.chaoxing.com/exam/test/look/", true},
		{"https://example.com/exam/test/look", false},
		{"https://mooc1-api.chaoxing.com/exam/test/submit", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isTaskEngineCompletedStudyURL(tc.rawURL); got != tc.expected {
			t.Fatalf("isTaskEngineCompletedStudyURL(%q) = %v, want %v", tc.rawURL, got, tc.expected)
		}
	}
}

func TestNormalizeTaskEngineStudyURL(t *testing.T) {
	cases := []struct {
		rawURL     string
		domainName string
		expected   string
	}{
		{"", "", ""},
		{"https://mooc1.chaoxing.com/work/do?foo=1", "", "https://mooc1.chaoxing.com/work/do?foo=1"},
		{"/userStudyPlan/detail?id=1", "https://task.chaoxing.com", "https://task.chaoxing.com/userStudyPlan/detail?id=1"},
		{"javascript:void(0)", "", ""},
	}
	for _, tc := range cases {
		if got := normalizeTaskEngineStudyURL(tc.rawURL, tc.domainName); got != tc.expected {
			t.Fatalf("normalizeTaskEngineStudyURL(%q, %q) = %q, want %q", tc.rawURL, tc.domainName, got, tc.expected)
		}
	}
}

func TestExtractTaskEngineDetailTimes(t *testing.T) {
	content := `<html><body><div>作答时间：2026年9月20日 08:00 至 2026-09-30 23:59</div>` +
		`<div>截止时间：09-30 23:59</div></body></html>`
	start, end := extractTaskEngineDetailTimes(content)
	if start != "2026-9-20 08:00" {
		t.Fatalf("start = %q", start)
	}
	if end != "09-30 23:59" {
		t.Fatalf("end = %q", end)
	}
	if start, end = extractTaskEngineDetailTimes("<html><body>没有时间信息</body></html>"); start != "" || end != "" {
		t.Fatalf("expected empty times, got %q / %q", start, end)
	}
}

var taskEngineTestUserSequence atomic.Uint64

func taskEngineTestUserID(t *testing.T) string {
	return fmt.Sprintf("%s-%d", t.Name(), taskEngineTestUserSequence.Add(1))
}

func TestTaskEngineCaptchaPropagatesThroughDashboard(t *testing.T) {
	for _, stage := range []struct {
		name string
		path string
	}{
		{"packages", "/v2/apis/active/getData"},
		{"landing", "/api/v1/middlePageApi/jumpStudyPlanList"},
		{"subpage", "/userStudyPlan/studyPlanSubPage"},
		{"groups", "/userStudyPlan/getGroupData"},
		{"plans", "/userStudyPlan/getPlanDataByGroupId"},
		{"study URL", "/userStudyPlan/getToStudyUrl"},
		{"detail page", "/work/detail"},
	} {
		t.Run(stage.name, func(t *testing.T) {
			taskUserID := taskEngineTestUserID(t)
			blocked := true
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				if blocked && req.URL.Path == stage.path {
					return learningTestCaptchaRedirect(req), nil
				}
				switch req.URL.Path {
				case "/antispiderShowVerify.ac":
					return learningTestResponse(req, http.StatusAccepted, "captcha"), nil
				case "/work/stu-work":
					return learningTestResponse(req, http.StatusServiceUnavailable, "unavailable"), nil
				case "/api/v1/middlePageApi/jumpStudyPlanList":
					return learningTestResponse(req, http.StatusOK, "<html></html>"), nil
				case "/userStudyPlan/studyPlanSubPage":
					return learningTestResponse(req, http.StatusOK, fmt.Sprintf(`<script>const eTaskUserId = %q;</script>`, taskUserID)), nil
				case "/userStudyPlan/getPlanDataByGroupId":
					return learningTestResponse(req, http.StatusOK, `{"data":[{"planId":301,"encryptPlanId":"deep-plan","name":"引擎作业","planType":4}]}`), nil
				case "/userStudyPlan/getToStudyUrl":
					return learningTestResponse(req, http.StatusOK, `{"result":true,"data":{"url":"https://mooc1.chaoxing.com/work/detail"}}`), nil
				case "/work/detail":
					return learningTestResponse(req, http.StatusOK, "<div>截止时间：2026-09-30 23:59</div>"), nil
				}
				return learningFixtureResponse(req)
			})
			out, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
			if !errors.Is(err, ErrCaptchaRequired) {
				t.Fatalf("deep captcha was hidden: %v, sections = %v", err, out.Errors)
			}
			if len(out.Homework)+len(out.Exams)+len(out.Activities)+len(out.Todo) != 0 {
				t.Fatalf("captcha returned a misleading partial dashboard: %+v", out)
			}

			blocked = false
			out, err = c.GetLearningDashboard(learningTestMobile, learningTestPassword)
			if err != nil {
				t.Fatalf("dashboard did not recover after captcha: %v", err)
			}
			assertLearningIDs(t, out.Homework, "task-engine-30-plan-301")
			if out.Homework[0].EndTime != at(2026, time.September, 30, 23, 59, 0) {
				t.Fatalf("recovered task has stale fallback details: %+v", out.Homework[0])
			}
			if len(out.Errors) == 0 {
				t.Fatal("ordinary homework failure was hidden after captcha recovery")
			}
		})
	}
}

func TestTaskEnginePreservesPlansFromSuccessfulGroups(t *testing.T) {
	c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/userStudyPlan/getGroupData":
			return learningTestResponse(req, http.StatusOK, `{"data":[{"encryptGroupId":"failed"},{"encryptGroupId":"good"}]}`), nil
		case "/userStudyPlan/getPlanDataByGroupId":
			if req.URL.Query().Get("encryGroupId") == "failed" {
				return learningTestResponse(req, http.StatusServiceUnavailable, learningTestPlans), nil
			}
		}
		return learningFixtureResponse(req)
	})
	out, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("failed plan group was hidden")
	}
	assertLearningIDs(t, out.Homework, "101", "task-engine-30-plan-301")
	assertLearningIDs(t, out.Exams, "201", "task-engine-30-plan-302")
}

func TestTaskEngineFallbackReportsErrors(t *testing.T) {
	for _, failure := range []struct {
		name   string
		path   string
		status int
		body   string
	}{
		{"landing unavailable", "/api/v1/middlePageApi/jumpStudyPlanList", http.StatusServiceUnavailable, "unavailable"},
		{"groups malformed", "/userStudyPlan/getGroupData", http.StatusOK, "not JSON"},
		{"plans unavailable", "/userStudyPlan/getPlanDataByGroupId", http.StatusServiceUnavailable, learningTestPlans},
	} {
		t.Run(failure.name, func(t *testing.T) {
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == failure.path || req.URL.Path == "/userStudyPlan/studyPlanSubPage" {
					return learningTestResponse(req, failure.status, failure.body), nil
				}
				return learningFixtureResponse(req)
			})
			out, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Errors) == 0 {
				t.Fatal("failed task expansion was reported as successful")
			}
			assertLearningIDs(t, out.Activities, "21", "task-engine-30")
			assertLearningIDs(t, out.Homework, "101")
			assertLearningIDs(t, out.Exams, "201")
		})
	}
}

func TestTaskEngineFailedDetailsAreRetriedRatherThanCached(t *testing.T) {
	const studyResponse = `{"result":true,"data":{"url":"https://mooc1.chaoxing.com/work/detail"}}`
	for _, failure := range []struct {
		name      string
		path      string
		status    int
		body      string
		captcha   bool
		transport bool
	}{
		{name: "study HTTP", path: "/userStudyPlan/getToStudyUrl", status: http.StatusServiceUnavailable, body: studyResponse},
		{name: "study JSON", path: "/userStudyPlan/getToStudyUrl", status: http.StatusOK, body: `{"result":`},
		{name: "study rejected", path: "/userStudyPlan/getToStudyUrl", status: http.StatusOK, body: `{"result":false}`},
		{name: "study transport", path: "/userStudyPlan/getToStudyUrl", transport: true},
		{name: "detail HTTP", path: "/work/detail", status: http.StatusServiceUnavailable, body: "unavailable"},
		{name: "detail captcha", path: "/work/detail", captcha: true},
	} {
		t.Run(failure.name, func(t *testing.T) {
			taskUserID := taskEngineTestUserID(t)
			phase := 0
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				if phase == 2 {
					return nil, errors.New("unexpected network request after successful detail fetch")
				}
				if req.URL.Path == "/antispiderShowVerify.ac" {
					return learningTestResponse(req, http.StatusAccepted, "captcha"), nil
				}
				if phase == 0 && req.URL.Path == failure.path {
					if failure.transport {
						return nil, errors.New("temporary transport failure")
					}
					if failure.captcha {
						return learningTestCaptchaRedirect(req), nil
					}
					return learningTestResponse(req, failure.status, failure.body), nil
				}
				switch req.URL.Path {
				case "/userStudyPlan/getToStudyUrl":
					return learningTestResponse(req, http.StatusOK, studyResponse), nil
				case "/work/detail":
					return learningTestResponse(req, http.StatusOK, "<div>开始时间：2026-09-20 08:00</div><div>截止时间：2026-09-30 23:59</div>"), nil
				default:
					return nil, fmt.Errorf("unexpected detail request: %s", req.URL)
				}
			})
			plan := map[string]interface{}{"planId": 301, "encryptPlanId": "retry-plan", "planType": 4, "name": "引擎作业"}
			if _, err := c.resolveTaskEnginePlanDetails(c.http, plan, taskUserID, "https://task.chaoxing.com/fallback"); err == nil || errors.Is(err, ErrCaptchaRequired) != failure.captcha {
				t.Fatalf("failed detail error = %v", err)
			}

			phase = 1
			details, err := c.resolveTaskEnginePlanDetails(c.http, plan, taskUserID, "https://task.chaoxing.com/fallback")
			if err != nil {
				t.Fatal(err)
			}
			item := taskEnginePlanItem(plan, learningCourse{CourseID: 10, ClassID: 20}, "30", "", details)
			if item.StartTime != at(2026, time.September, 20, 8, 0, 0) || item.EndTime != at(2026, time.September, 30, 23, 59, 0) || item.Link != "https://mooc1.chaoxing.com/work/detail" {
				t.Fatalf("retry reused failed details: %+v", item)
			}

			phase = 2
			cached, err := c.resolveTaskEnginePlanDetails(c.http, plan, taskUserID, "https://task.chaoxing.com/fallback")
			if err != nil || cached != details {
				t.Fatalf("successful details were not retained: %+v, %v", cached, err)
			}
		})
	}
}

func TestTaskEngineOptionalDetailsRemainSuccessful(t *testing.T) {
	t.Run("missing encrypted plan", func(t *testing.T) {
		c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("unexpected request for optional details: %s", req.URL)
		})
		plan := map[string]interface{}{"planId": 301, "planType": 5, "name": "已完成考试", "hyperLink": "https://mooc1-api.chaoxing.com/exam/test/look"}
		details, err := c.resolveTaskEnginePlanDetails(c.http, plan, taskEngineTestUserID(t), "https://task.chaoxing.com/fallback")
		if err != nil {
			t.Fatal(err)
		}
		item := taskEnginePlanItem(plan, learningCourse{CourseID: 10, ClassID: 20}, "30", "", details)
		if !item.Finished || item.Pending {
			t.Fatalf("completed optional plan became pending: %+v", item)
		}
	})

	t.Run("detail without dates", func(t *testing.T) {
		c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/userStudyPlan/getToStudyUrl" {
				return learningTestResponse(req, http.StatusOK, `{"result":true,"data":{"url":"https://mooc1.chaoxing.com/work/detail"}}`), nil
			}
			if req.URL.Path == "/work/detail" {
				return learningTestResponse(req, http.StatusOK, "<html><body>没有期限</body></html>"), nil
			}
			return nil, fmt.Errorf("unexpected request for optional dates: %s", req.URL)
		})
		plan := map[string]interface{}{"planId": 301, "encryptPlanId": "optional-dates", "planType": 4, "name": "作业"}
		details, err := c.resolveTaskEnginePlanDetails(c.http, plan, taskEngineTestUserID(t), "https://task.chaoxing.com/fallback")
		if err != nil {
			t.Fatal(err)
		}
		item := taskEnginePlanItem(plan, learningCourse{CourseID: 10, ClassID: 20}, "30", "", details)
		if item.EndTime != 0 || !item.Pending {
			t.Fatalf("missing optional dates changed the task state: %+v", item)
		}
	})

	for _, path := range []string{"/userStudyPlan/getGroupData", "/userStudyPlan/getPlanDataByGroupId"} {
		t.Run("empty "+path, func(t *testing.T) {
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == path {
					return learningTestResponse(req, http.StatusOK, `{"data":[]}`), nil
				}
				return learningFixtureResponse(req)
			})
			items, err := c.getTaskEngineTasksForCourse(c.http, learningCourse{CourseID: 10, ClassID: 20})
			if err != nil {
				t.Fatal(err)
			}
			assertLearningIDs(t, items, "task-engine-30")
		})
	}
}

func TestTaskEngineDetailFailurePreservesPlanResults(t *testing.T) {
	c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/userStudyPlan/getPlanDataByGroupId":
			return learningTestResponse(req, http.StatusOK, `{"data":[{"planId":301,"encryptPlanId":"failed-plan","name":"引擎作业","planType":4},{"planId":302,"name":"引擎考试","planType":5}]}`), nil
		case "/userStudyPlan/getToStudyUrl":
			return learningTestResponse(req, http.StatusServiceUnavailable, "unavailable"), nil
		}
		return learningFixtureResponse(req)
	})
	out, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("plan detail failure was hidden")
	}
	assertLearningIDs(t, out.Homework, "101", "task-engine-30-plan-301")
	assertLearningIDs(t, out.Exams, "201", "task-engine-30-plan-302")
}

func TestTaskEnginePlanIDAvoidsScientificNotation(t *testing.T) {
	// JSON numbers decode to float64; a 7+ digit planId must not become "6.00123456e+08".
	plan := map[string]interface{}{"planId": float64(600123456), "name": "大号任务", "planType": 4}
	item := taskEnginePlanItem(plan, learningCourse{CourseID: 10, ClassID: 20}, "30", "", taskEnginePlanDetails{})
	if item.ID != "task-engine-30-plan-600123456" {
		t.Fatalf("plan ID = %q, want task-engine-30-plan-600123456", item.ID)
	}
}
