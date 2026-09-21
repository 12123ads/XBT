package xxt

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	learningTestMobile   = "learning-test-mobile"
	learningTestPassword = "learning-test-password"
	learningTestHomework = `<ul class="nav"><li data="/work?courseId=10&amp;classId=20&amp;workId=101"><div role="option"><p>普通作业</p><span class="status">待完成</span><span>课程</span></div></li></ul>`
	learningTestExams    = `<ul class="ks_list"><li data="/exam?courseId=10&amp;classId=20&amp;examId=201"><dl><dt>普通考试</dt></dl><span class="ks_state">待完成</span></li></ul>`
	learningTestCourses  = `{"channelList":[{"key":20,"content":{"id":20,"course":{"data":[{"id":10,"name":"课程"}]}}}]}`
	learningTestActives  = `{"data":{"activeList":[{"id":21,"activeType":4,"nameOne":"课堂活动","status":1}]}}`
	learningTestPackages = `{"data":[{"id":30,"name":"课程任务包","planCount":2}]}`
	learningTestGroups   = `{"data":[{"encryptGroupId":"group-30"}]}`
	learningTestPlans    = `{"data":[{"planId":301,"name":"引擎作业","planType":4},{"planId":302,"name":"引擎考试","planType":5}]}`
)

type learningTestTransport func(*http.Request) (*http.Response, error)

func (f learningTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type learningObservedBody struct {
	io.ReadCloser
	onClose func()
}

func (b *learningObservedBody) Close() error {
	err := b.ReadCloser.Close()
	b.onClose()
	return err
}

func newLearningTestClient(t *testing.T, transport learningTestTransport) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(&url.URL{Scheme: "https", Host: "mooc1.chaoxing.com", Path: "/"}, []*http.Cookie{
		{Name: "learning_session", Value: "existing", Domain: ".chaoxing.com", Path: "/", Secure: true},
	})
	return &Client{
		mobileUA: "learning-test",
		http:     &http.Client{Transport: transport, Timeout: 5 * time.Second},
		sessions: map[string]*Session{
			learningTestMobile: {
				Mobile:      learningTestMobile,
				Password:    learningTestPassword,
				Jar:         jar,
				LastLoginAt: time.Now(),
			},
		},
	}
}

func learningTestResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func learningTestCaptchaRedirect(req *http.Request) *http.Response {
	resp := learningTestResponse(req, http.StatusFound, "")
	resp.Header.Set("Location", antispiderVerifyPage)
	return resp
}

func learningFixtureResponse(req *http.Request) (*http.Response, error) {
	var body string
	switch req.URL.Path {
	case "/work/stu-work":
		body = learningTestHomework
	case "/exam-ans/exam/phone/examcode":
		body = learningTestExams
	case "/exam-ans/exam/test/examcode/examlist":
		body = "<html></html>"
	case "/mycourse/backclazzdata":
		body = learningTestCourses
	case "/v2/apis/active/student/activelist":
		body = learningTestActives
	case "/v2/apis/active/getData":
		body = learningTestPackages
	case "/api/v1/middlePageApi/jumpStudyPlanList":
		body = `<script>var eTaskUserId = "learning-user";</script>`
	case "/userStudyPlan/getGroupData":
		body = learningTestGroups
	case "/userStudyPlan/getPlanDataByGroupId":
		body = learningTestPlans
	default:
		return nil, fmt.Errorf("unexpected learning request: %s", req.URL)
	}
	return learningTestResponse(req, http.StatusOK, body), nil
}

func assertLearningIDs(t *testing.T, items []LearningItem, want ...string) {
	t.Helper()
	got := make([]string, len(items))
	for i, item := range items {
		got[i] = item.ID
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("item IDs = %v, want %v", got, want)
	}
}

func TestLearningDashboardCombinesConcurrentSources(t *testing.T) {
	homeworkStarted := make(chan struct{})
	examsStarted := make(chan struct{})
	engineRead := make(chan struct{})
	releaseOrdinary := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseOrdinary) }) }
	t.Cleanup(release)

	c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/work/stu-work":
			close(homeworkStarted)
			<-releaseOrdinary
		case "/exam-ans/exam/phone/examcode":
			close(examsStarted)
			<-releaseOrdinary
		}
		resp, err := learningFixtureResponse(req)
		if err == nil && req.URL.Path == "/userStudyPlan/getPlanDataByGroupId" {
			resp.Body = &learningObservedBody{ReadCloser: resp.Body, onClose: func() { close(engineRead) }}
		}
		return resp, err
	})
	type result struct {
		dashboard LearningDashboard
		err       error
	}
	completed := make(chan result, 1)
	go func() {
		dashboard, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
		completed <- result{dashboard: dashboard, err: err}
	}()

	for _, barrier := range []struct {
		name string
		ch   <-chan struct{}
	}{
		{"homework started", homeworkStarted},
		{"exams started", examsStarted},
		{"engine response consumed", engineRead},
	} {
		select {
		case <-barrier.ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", barrier.name)
		}
	}
	// Both ordinary sources finish after the engine response, not before it.
	release()
	select {
	case result := <-completed:
		if result.err != nil || len(result.dashboard.Errors) != 0 {
			t.Fatalf("dashboard error = %v, sections = %v", result.err, result.dashboard.Errors)
		}
		assertLearningIDs(t, result.dashboard.Homework, "101", "task-engine-30-plan-301")
		assertLearningIDs(t, result.dashboard.Exams, "201", "task-engine-30-plan-302")
		assertLearningIDs(t, result.dashboard.Activities, "21")
		assertLearningIDs(t, result.dashboard.Todo, "101", "201", "21", "task-engine-30-plan-301", "task-engine-30-plan-302")
	case <-time.After(5 * time.Second):
		t.Fatal("dashboard did not finish after releasing ordinary sources")
	}
}

func TestLearningDashboardPreservesPartialCourseSources(t *testing.T) {
	for _, tc := range []struct {
		name           string
		failActivities bool
		failEngine     bool
	}{
		{name: "activities fail", failActivities: true},
		{name: "task engine fails", failEngine: true},
		{name: "both fail", failActivities: true, failEngine: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Path {
				case "/work/stu-work", "/exam-ans/exam/phone/examcode":
					return learningTestResponse(req, http.StatusOK, "<html></html>"), nil
				case "/v2/apis/active/student/activelist":
					if tc.failActivities {
						return learningTestResponse(req, http.StatusBadGateway, learningTestActives), nil
					}
				case "/v2/apis/active/getData":
					if tc.failEngine {
						return learningTestResponse(req, http.StatusServiceUnavailable, learningTestPackages), nil
					}
				}
				return learningFixtureResponse(req)
			})
			out, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Errors) == 0 {
				t.Fatal("failed course sources were reported as an empty success")
			}
			if tc.failEngine {
				assertLearningIDs(t, out.Homework)
				assertLearningIDs(t, out.Exams)
			} else {
				assertLearningIDs(t, out.Homework, "task-engine-30-plan-301")
				assertLearningIDs(t, out.Exams, "task-engine-30-plan-302")
			}
			if tc.failActivities {
				assertLearningIDs(t, out.Activities)
			} else {
				assertLearningIDs(t, out.Activities, "21")
			}
		})
	}
}

func TestLearningActivitiesPreservesSuccessfulCoursesAndErrors(t *testing.T) {
	activityFailure := errors.New("activity source unavailable")
	engineFailure := errors.New("task engine source unavailable")
	c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/mycourse/backclazzdata" {
			return learningTestResponse(req, http.StatusOK, `{"channelList":[{"content":{"id":20,"course":{"data":[{"id":10,"name":"课程一"}]} }},{"content":{"id":22,"course":{"data":[{"id":11,"name":"课程二"}]}}}]}`), nil
		}
		if req.URL.Query().Get("courseId") == "11" {
			switch req.URL.Path {
			case "/v2/apis/active/student/activelist":
				return nil, activityFailure
			case "/v2/apis/active/getData":
				return nil, engineFailure
			}
		}
		return learningFixtureResponse(req)
	})
	out, err := c.GetLearningActivities(c.http)
	if !errors.Is(err, activityFailure) || !errors.Is(err, engineFailure) {
		t.Fatalf("course errors were lost: %v", err)
	}
	assertLearningIDs(t, out.Homework, "task-engine-30-plan-301")
	assertLearningIDs(t, out.Exams, "task-engine-30-plan-302")
	assertLearningIDs(t, out.Activities, "21")
}

func TestLearningDashboardKeepsPartialExamResults(t *testing.T) {
	c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/exam-ans/exam/test/examcode/examlist" {
			return learningTestResponse(req, http.StatusServiceUnavailable, "<html></html>"), nil
		}
		return learningFixtureResponse(req)
	})
	out, err := c.GetLearningDashboard(learningTestMobile, learningTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Errors) == 0 {
		t.Fatal("failed exam source was hidden by the other successful source")
	}
	assertLearningIDs(t, out.Exams, "201", "task-engine-30-plan-302")
}

func TestLearningRequestsRequireSuccessfulHTTPStatus(t *testing.T) {
	for _, fetch := range []struct {
		name string
		body string
		call func(*Client) error
	}{
		{"task page", learningTestGroups, func(c *Client) error {
			_, err := c.fetchLearningPage(c.http, http.MethodGet, "https://task.chaoxing.com/page", "", false)
			return err
		}},
		{"html", learningTestHomework, func(c *Client) error {
			_, err := c.getHTML(c.http, "https://mooc1-api.chaoxing.com/work/stu-work", "learning-test")
			return err
		}},
		{"courses", learningTestCourses, func(c *Client) error {
			_, err := c.getLearningCourses(c.http)
			return err
		}},
		{"activities", learningTestActives, func(c *Client) error {
			_, err := c.getLearningActivitiesForCourse(c.http, learningCourse{CourseID: 10, ClassID: 20})
			return err
		}},
	} {
		t.Run(fetch.name, func(t *testing.T) {
			for _, response := range []struct {
				name    string
				status  int
				captcha bool
			}{
				{"redirect without destination", http.StatusFound, false},
				{"not found", http.StatusNotFound, false},
				{"unavailable", http.StatusServiceUnavailable, false},
				{"captcha before HTTP status", http.StatusServiceUnavailable, true},
			} {
				t.Run(response.name, func(t *testing.T) {
					c := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
						if response.captcha && req.URL.Path != "/antispiderShowVerify.ac" {
							return learningTestCaptchaRedirect(req), nil
						}
						return learningTestResponse(req, response.status, fetch.body), nil
					})
					err := fetch.call(c)
					if err == nil || errors.Is(err, ErrCaptchaRequired) != response.captcha {
						t.Fatalf("HTTP %d (captcha %v) error = %v", response.status, response.captcha, err)
					}
				})
			}
		})
	}
}
