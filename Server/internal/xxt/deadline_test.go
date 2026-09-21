package xxt

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestParseLearningDeadlineAbsolute(t *testing.T) {
	got := parseLearningDeadline("剩余时间 2026-09-30 23:59")
	want := time.Date(2026, time.September, 30, 23, 59, 0, 0, time.Local).UnixMilli()
	if got != want {
		t.Fatalf("absolute deadline = %d, want %d", got, want)
	}
}

func TestParseLearningDeadlineRange(t *testing.T) {
	got := parseLearningDeadline("作答时间：2026-09-01 08:00 至 2026-10-08 23:59")
	want := time.Date(2026, time.October, 8, 23, 59, 0, 0, time.Local).UnixMilli()
	if got != want {
		t.Fatalf("range deadline = %d, want %d (should take the end)", got, want)
	}
}

func TestParseLearningDeadlineRelative(t *testing.T) {
	span := 2*24*time.Hour + time.Hour + 30*time.Minute
	before := time.Now().Add(span).UnixMilli()
	got := parseLearningDeadline("剩余2天1小时30分")
	after := time.Now().Add(span).UnixMilli()
	if got < before-2000 || got > after+2000 {
		t.Fatalf("relative deadline = %d, expected around %d..%d", got, before, after)
	}
}

func TestParseLearningDeadlineUnknownOrExpired(t *testing.T) {
	for _, text := range []string{"", "已过期", "已结束", "无期限", "进行中"} {
		if got := parseLearningDeadline(text); got != 0 {
			t.Fatalf("parseLearningDeadline(%q) = %d, want 0", text, got)
		}
	}
}

func TestGetLearningHomeworkSetsEndTime(t *testing.T) {
	client := newLearningTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/work/stu-work" {
			body := `<ul class="nav"><li data="/work?courseId=10&amp;classId=20&amp;workId=777"><div role="option"><p>带截止作业</p><span class="status">待完成</span><span>课程</span><span class="fr">剩余时间 2026-11-05 23:59</span></div></li></ul>`
			return learningTestResponse(req, http.StatusOK, body), nil
		}
		return nil, fmt.Errorf("unexpected learning request: %s", req.URL)
	})
	items, err := client.GetLearningHomework(client.http)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 homework item, got %d", len(items))
	}
	want := time.Date(2026, time.November, 5, 23, 59, 0, 0, time.Local).UnixMilli()
	if items[0].EndTime != want {
		t.Fatalf("homework EndTime = %d, want %d", items[0].EndTime, want)
	}
}
