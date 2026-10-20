package service

import (
	"errors"
	"testing"

	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

func TestSignExecuteUsesCachedExtWhenMetadataMisses(t *testing.T) {
	cases := []struct {
		name      string
		detail    xxt.SignDetail
		detailErr error
	}{
		{name: "metadata request failed", detailErr: errors.New("upstream unavailable")},
		{name: "metadata returned empty ext", detail: xxt.SignDetail{SignType: xxt.SignNormal}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			if err := database.Create(&model.SignActivity{ActivityID: 100, ExtJSON: "cached-ext"}).Error; err != nil {
				t.Fatal(err)
			}
			var preSignExt string
			upstream := &signAuthorizationUpstream{
				detail:    tc.detail,
				detailErr: tc.detailErr,
				preSign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _, _ string) (xxt.PreSignResult, error) {
					preSignExt = fixed.Ext
					return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
				},
				sign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
					if fixed.Ext != "cached-ext" {
						t.Fatalf("sign request lost cached ext: %+v", fixed)
					}
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
			if err != nil || !result.Success {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if preSignExt != "cached-ext" {
				t.Fatalf("preSign ext = %q, want cached-ext", preSignExt)
			}
		})
	}
}

func TestSignExecutePrefersLiveExtOverCachedExt(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	if err := database.Create(&model.SignActivity{ActivityID: 100, ExtJSON: "cached-ext"}).Error; err != nil {
		t.Fatal(err)
	}
	var preSignExt string
	upstream := &signAuthorizationUpstream{
		detail: xxt.SignDetail{Ext: "live-ext", SignType: xxt.SignNormal},
		preSign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _, _ string) (xxt.PreSignResult, error) {
			preSignExt = fixed.Ext
			return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
		},
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
	if err != nil || !result.Success {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if preSignExt != "live-ext" {
		t.Fatalf("preSign ext = %q, want live-ext", preSignExt)
	}
}
