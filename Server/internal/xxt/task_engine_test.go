package xxt

import (
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
		{"full datetime", "2026-03-15 09:30", func(v int64) bool { return v == at(year, time.March, 15, 9, 30, 0) }},
		{"full datetime seconds", "2026-03-15 09:30:05", func(v int64) bool { return v == at(year, time.March, 15, 9, 30, 5) }},
		{"chinese text", "2026年3月15日 09:30", func(v int64) bool { return v == at(year, time.March, 15, 9, 30, 0) }},
		{"dotted", "2026.3.15 09:30", func(v int64) bool { return v == at(year, time.March, 15, 9, 30, 0) }},
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
