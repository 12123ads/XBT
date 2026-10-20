package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

type handlerSignUpstream struct {
	scope       bool
	scopeErr    error
	activities  []xxt.Active
	detail      xxt.SignDetail
	detailErr   error
	faceFlag    bool
	detailCalls int
	signed      []int64
	onSign      func(xxt.FixedParams) error
	signResults []xxt.SignResult
}

func (u *handlerSignUpstream) GetActives(string, string, int64, int64) ([]xxt.Active, error) {
	return u.activities, nil
}

func (u *handlerSignUpstream) GetSignDetail(string, string, int64, int64, int64, int64) (xxt.SignDetail, error) {
	u.detailCalls++
	return u.detail, u.detailErr
}

func (u *handlerSignUpstream) GetFaceFlag(string, string, int64, int64, int64, int64) bool {
	return u.faceFlag
}

func (u *handlerSignUpstream) HasActivityInCourse(string, string, int64, int64, int64) (bool, error) {
	return u.scope, u.scopeErr
}

func (u *handlerSignUpstream) PreSign(string, string, xxt.FixedParams, int, string, string) (xxt.PreSignResult, error) {
	return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
}

func (u *handlerSignUpstream) Sign(_ string, _ string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
	u.signed = append(u.signed, fixed.UID)
	if u.onSign != nil {
		if err := u.onSign(fixed); err != nil {
			return xxt.SignResult{}, err
		}
	}
	if len(u.signResults) > 0 {
		result := u.signResults[0]
		if len(u.signResults) > 1 {
			u.signResults = u.signResults[1:]
		}
		return result, nil
	}
	return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
}

func (u *handlerSignUpstream) UploadPanFile(string, string, string, string, []byte) (string, error) {
	return "object-id", nil
}

func (u *handlerSignUpstream) UploadFaceTemp(string, string, string, string, []byte) (string, error) {
	return "face-object-id", nil
}

func (u *handlerSignUpstream) CheckFaceResult(string, string, int64, string, string) (xxt.FaceCheckResult, error) {
	return xxt.FaceCheckResult{}, nil
}

func handlerSignFixture(t *testing.T) (*gorm.DB, *gin.Engine, *handlerSignUpstream) {
	t.Helper()
	database, router, upstream, _ := handlerSignFixtureParts(t)
	return database, router, upstream
}

func handlerSignFixtureParts(t *testing.T) (*gorm.DB, *gin.Engine, *handlerSignUpstream, *SignHandler) {
	t.Helper()
	database := newHandlerAccessDB(t)
	cc := service.NewCredentialCrypto("handler-sign-test")
	cipher, err := cc.Encrypt("password")
	if err != nil {
		t.Fatal(err)
	}
	for uid := int64(1); uid <= 3; uid++ {
		user := model.User{UID: uid, Mobile: fmt.Sprintf("138%08d", uid), Name: fmt.Sprintf("user-%d", uid), CredentialCipher: cipher, Permission: 1}
		if err := database.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&model.Whitelist{Mobile: user.Mobile, Permission: 1}).Error; err != nil {
			t.Fatal(err)
		}
		classID := int64(20)
		if uid == 3 {
			classID = 21
		}
		if err := database.Create(&model.UserCourse{UserUID: uid, CourseID: 10, ClassID: classID, IsSelected: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Create(&model.Course{CourseID: 10, ClassID: 20, Name: "测试课程"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.SignActivityScope{ActivityID: 100, CourseID: 10, ClassID: 20}).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &handlerSignUpstream{}
	h := NewSignHandler(database, upstream, cc, service.NewSignService(database, upstream, cc, nil), 5, nil)
	router := gin.New()
	authed := router.Group("", func(c *gin.Context) { c.Set(common.CtxUserUID, int64(1)); c.Next() })
	authed.GET("/activities", h.Activities)
	authed.GET("/im/status", h.IMStatus)
	authed.GET("/classmates", h.Classmates)
	authed.POST("/check", h.Check)
	authed.POST("/execute", h.Execute)
	authed.POST("/shares", h.CreateShare)
	authed.DELETE("/users/:id", NewWhitelistHandler(database).DeleteUser)
	authed.POST("/accounts/:uid/sync", NewAdminAccountHandler(database, nil, nil).SyncUserCourses)
	router.GET("/public/:token", h.GetShare)
	router.POST("/public/:token", h.ExecuteShare)
	return database, router, upstream, h
}

func TestActivitiesReturnsSignMetadataAndKeepsLegacyFields(t *testing.T) {
	_, router, upstream := handlerSignFixture(t)
	upstream.activities = []xxt.Active{{ActiveID: 100, Name: "签到"}}
	upstream.detail = xxt.SignDetail{
		StartTime:          1700000000000,
		EndTime:            1700000600000,
		SignType:           xxt.SignLocation,
		IfRefreshEWM:       true,
		IfPhoto:            true,
		IfFace:             true,
		IfNeedVCode:        true,
		IfOpenAddress:      true,
		LocationLatitude:   31.2304,
		LocationLongitude:  121.4737,
		LocationRange:      200,
		SignInID:           101,
		SignOutID:          102,
		SignOutPublishTime: 1700000700000,
	}

	response := accessRequest(t, router, http.MethodGet, "/activities", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("activities status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code int `json:"code"`
		Data []struct {
			Activities []struct {
				ActiveID          int64   `json:"active_id"`
				StartTime         int64   `json:"start_time"`
				EndTime           int64   `json:"end_time"`
				SignType          int     `json:"sign_type"`
				IfRefreshEWM      bool    `json:"if_refresh_ewm"`
				IfPhoto           bool    `json:"if_photo"`
				IfFace            bool    `json:"if_face"`
				IfNeedVCode       bool    `json:"if_need_vcode"`
				IfOpenAddress     bool    `json:"if_open_address"`
				LocationLatitude  float64 `json:"location_latitude"`
				LocationLongitude float64 `json:"location_longitude"`
				LocationRange     int     `json:"location_range"`
			} `json:"activities"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != 0 || len(payload.Data) != 1 || len(payload.Data[0].Activities) != 1 {
		t.Fatalf("unexpected activities payload: %s", response.Body.String())
	}
	item := payload.Data[0].Activities[0]
	if item.ActiveID != 100 || item.StartTime != 1700000000000 || item.EndTime != 1700000600000 || item.SignType != xxt.SignLocation {
		t.Fatalf("legacy identity fields changed: %+v", item)
	}
	if !item.IfRefreshEWM || !item.IfPhoto || !item.IfFace {
		t.Fatalf("legacy flags changed: %+v", item)
	}
	if !item.IfNeedVCode || !item.IfOpenAddress {
		t.Fatalf("metadata flags missing: %+v", item)
	}
	if item.LocationLatitude != 31.2304 || item.LocationLongitude != 121.4737 || item.LocationRange != 200 {
		t.Fatalf("location metadata missing: %+v", item)
	}
	if upstream.detailCalls != 1 {
		t.Fatalf("detail calls=%d, want 1", upstream.detailCalls)
	}
}

func createHandlerSignShare(t *testing.T, router http.Handler) string {
	t.Helper()
	response := accessRequest(t, router, http.MethodPost, "/shares", gin.H{"activity_id": 100, "course_id": 10, "class_id": 20, "sign_type": xxt.SignNormal, "end_time": time.Now().Add(time.Hour).UnixMilli()})
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || payload.Data.Token == "" {
		t.Fatalf("create authorized share: status=%d body=%s", response.Code, response.Body.String())
	}
	return payload.Data.Token
}

func TestSignRoutesRejectUnauthorizedTargetsAndActivityPairs(t *testing.T) {
	database, router, upstream := handlerSignFixture(t)
	if err := database.Create(&model.SignRecord{UserUID: 3, ActivityID: 100, SourceUID: 3, SignTimeMS: 123}).Error; err != nil {
		t.Fatal(err)
	}
	mates := accessRequest(t, router, http.MethodGet, "/classmates?course_id=10&class_id=20", nil)
	var payload struct {
		Data []struct {
			UID int64 `json:"uid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(mates.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if mates.Code != http.StatusOK || len(payload.Data) != 1 || payload.Data[0].UID != 2 {
		t.Fatalf("classmates leaked another pair: status=%d body=%s", mates.Code, mates.Body.String())
	}
	if got := accessRequest(t, router, http.MethodGet, "/classmates?course_id=10&class_id=21", nil); got.Code != http.StatusForbidden {
		t.Fatalf("unselected pair exposed classmates: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := accessRequest(t, router, http.MethodGet, "/classmates?course_id=0&class_id=20", nil); got.Code != http.StatusBadRequest {
		t.Fatalf("nonpositive course pair accepted: status=%d", got.Code)
	}
	if got := accessRequest(t, router, http.MethodPost, "/check", gin.H{"activity_id": 100, "user_ids": []int64{2}}); got.Code != http.StatusBadRequest {
		t.Fatalf("check accepted missing scope: status=%d", got.Code)
	}
	for _, path := range []string{"/check", "/execute"} {
		got := accessRequest(t, router, http.MethodPost, path, gin.H{"activity_id": 100, "course_id": 10, "class_id": 20, "user_ids": []int64{3}, "target_uid": 3})
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s disclosed an already-signed out-of-pair target: status=%d body=%s", path, got.Code, got.Body.String())
		}
	}
	for _, path := range []string{"/check", "/execute", "/shares"} {
		got := accessRequest(t, router, http.MethodPost, path, gin.H{"activity_id": 999, "course_id": 10, "class_id": 20, "target_uid": 2, "user_ids": []int64{2}, "end_time": time.Now().Add(time.Hour).UnixMilli()})
		if got.Code != http.StatusForbidden {
			t.Fatalf("%s trusted caller-supplied activity scope: status=%d body=%s", path, got.Code, got.Body.String())
		}
	}
	upstream.scopeErr = errors.New("activity upstream unavailable")
	for _, path := range []string{"/check", "/execute", "/shares"} {
		got := accessRequest(t, router, http.MethodPost, path, gin.H{"activity_id": 999, "course_id": 10, "class_id": 20, "target_uid": 2, "end_time": time.Now().Add(time.Hour).UnixMilli()})
		if got.Code != http.StatusBadGateway {
			t.Fatalf("%s did not distinguish upstream authorization outage: status=%d body=%s", path, got.Code, got.Body.String())
		}
	}
	if len(upstream.signed) != 0 {
		t.Fatalf("rejected requests performed upstream sign: %v", upstream.signed)
	}
	if got := accessRequest(t, router, http.MethodPost, "/execute", gin.H{"activity_id": 100, "course_id": 10, "class_id": 20, "target_uid": 2}); got.Code != http.StatusOK {
		t.Fatalf("valid cached scope rejected: status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestSharesAndStoredCredentialOperationsHonorWhitelistDeletion(t *testing.T) {
	database, router, upstream := handlerSignFixture(t)
	token := createHandlerSignShare(t, router)
	var peer model.Whitelist
	if err := database.Where("mobile = ?", "13800000002").Take(&peer).Error; err != nil {
		t.Fatal(err)
	}
	if got := accessRequest(t, router, http.MethodDelete, fmt.Sprintf("/users/%d", peer.ID), nil); got.Code != http.StatusOK {
		t.Fatalf("revoke peer: status=%d body=%s", got.Code, got.Body.String())
	}
	mates := accessRequest(t, router, http.MethodGet, "/classmates?course_id=10&class_id=20", nil)
	var classmates struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(mates.Body.Bytes(), &classmates); err != nil || mates.Code != http.StatusOK || len(classmates.Data) != 0 {
		t.Fatalf("classmates retained revoked peer: status=%d body=%s err=%v", mates.Code, mates.Body.String(), err)
	}
	if got := accessRequest(t, router, http.MethodPost, "/accounts/2/sync", nil); got.Code != http.StatusForbidden {
		t.Fatalf("admin credential operation reached revoked account: status=%d body=%s", got.Code, got.Body.String())
	}
	if got := accessRequest(t, router, http.MethodGet, "/public/"+token, nil); got.Code != http.StatusOK {
		t.Fatalf("revoking peer incorrectly invalidated creator share: status=%d", got.Code)
	}
	got := accessRequest(t, router, http.MethodPost, "/public/"+token, gin.H{})
	var summary struct {
		Data struct {
			TargetCount int  `json:"target_count"`
			Used        bool `json:"used"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if got.Code != http.StatusOK || summary.Data.TargetCount != 1 || !summary.Data.Used || !reflect.DeepEqual(upstream.signed, []int64{1}) {
		t.Fatalf("share included revoked or other-pair peer: status=%d body=%s signed=%v", got.Code, got.Body.String(), upstream.signed)
	}
	token = createHandlerSignShare(t, router)
	var creator model.Whitelist
	if err := database.Where("mobile = ?", "13800000001").Take(&creator).Error; err != nil {
		t.Fatal(err)
	}
	if got := accessRequest(t, router, http.MethodDelete, fmt.Sprintf("/users/%d", creator.ID), nil); got.Code != http.StatusOK {
		t.Fatalf("revoke creator: status=%d", got.Code)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := accessRequest(t, router, method, "/public/"+token, gin.H{}); got.Code != http.StatusGone {
			t.Fatalf("anonymous %s survived creator revocation: status=%d body=%s", method, got.Code, got.Body.String())
		}
	}
	if !reflect.DeepEqual(upstream.signed, []int64{1}) {
		t.Fatalf("revoked creator continued upstream sign: %v", upstream.signed)
	}
}

func TestShareStopsWhenCreatorLosesAccessDuringExecution(t *testing.T) {
	for _, revokeAccount := range []bool{true, false} {
		name := "course unselected"
		if revokeAccount {
			name = "account inactive"
		}
		t.Run(name, func(t *testing.T) {
			database, router, upstream := handlerSignFixture(t)
			token := createHandlerSignShare(t, router)
			upstream.onSign = func(fixed xxt.FixedParams) error {
				if fixed.UID != 1 {
					t.Fatalf("creator must run first: %+v", fixed)
				}
				if revokeAccount {
					return database.Model(&model.Whitelist{}).Where("mobile = ?", "13800000001").Update("permission", 0).Error
				}
				return database.Model(&model.UserCourse{}).Where("user_uid = ?", 1).Update("is_selected", false).Error
			}
			got := accessRequest(t, router, http.MethodPost, "/public/"+token, gin.H{})
			if got.Code != http.StatusGone || !reflect.DeepEqual(upstream.signed, []int64{1}) {
				t.Fatalf("share continued after creator lost access: status=%d body=%s signed=%v", got.Code, got.Body.String(), upstream.signed)
			}
			var share model.SignShare
			if err := database.Where("token_hash = ?", hashSignShareToken(token)).Take(&share).Error; err != nil || share.UsedAt != nil {
				t.Fatalf("interrupted share was marked used: share=%+v err=%v", share, err)
			}
		})
	}
}

func TestShareReportsTargetRevocationWithoutUsingItsCredentials(t *testing.T) {
	database, router, upstream := handlerSignFixture(t)
	token := createHandlerSignShare(t, router)
	upstream.onSign = func(fixed xxt.FixedParams) error {
		if fixed.UID != 1 {
			t.Fatalf("revoked target reached upstream: %+v", fixed)
		}
		return database.Model(&model.Whitelist{}).Where("mobile = ?", "13800000002").Update("permission", 0).Error
	}
	got := accessRequest(t, router, http.MethodPost, "/public/"+token, gin.H{})
	var summary struct {
		Data struct {
			SuccessCount int  `json:"success_count"`
			FailedCount  int  `json:"failed_count"`
			Used         bool `json:"used"`
		} `json:"data"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if got.Code != http.StatusOK || summary.Data.SuccessCount != 1 || summary.Data.FailedCount != 1 || summary.Data.Used || !reflect.DeepEqual(upstream.signed, []int64{1}) {
		t.Fatalf("mid-batch target revocation was not a safe partial failure: status=%d body=%s signed=%v", got.Code, got.Body.String(), upstream.signed)
	}
}

func TestExecuteReturnsTypedErrorCodesAndRetryFlags(t *testing.T) {
	cases := []struct {
		name        string
		results     []xxt.SignResult
		wantCalls   int
		wantSuccess bool
		wantCode    string
		wantRetry   bool
		wantDist    float64
	}{
		{
			name:        "success2",
			results:     []xxt.SignResult{{Kind: xxt.SignResultSuccess2}},
			wantCalls:   1,
			wantSuccess: true,
		},
		{
			name:      "captcha",
			results:   []xxt.SignResult{{Kind: xxt.SignResultCaptchaRequired, Enc2: "abc"}},
			wantCalls: 1,
			wantCode:  service.SignErrorCaptchaRequired,
			wantRetry: true,
		},
		{
			name:      "qr expired",
			results:   []xxt.SignResult{{Kind: xxt.SignResultQRExpired}},
			wantCalls: 1,
			wantCode:  service.SignErrorQRExpired,
		},
		{
			name: "location exhausted",
			results: []xxt.SignResult{
				{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 12},
				{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 12},
			},
			wantCalls: 2,
			wantCode:  service.SignErrorLocationOutRange,
			wantDist:  12,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, router, upstream := handlerSignFixture(t)
			upstream.signResults = tc.results
			response := accessRequest(t, router, http.MethodPost, "/execute", gin.H{
				"activity_id": 100, "course_id": 10, "class_id": 20, "target_uid": 2, "sign_type": xxt.SignNormal,
			})
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			var payload struct {
				Data service.SignExecuteResult `json:"data"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if len(upstream.signed) != tc.wantCalls {
				t.Fatalf("upstream calls = %d, want %d", len(upstream.signed), tc.wantCalls)
			}
			if payload.Data.Success != tc.wantSuccess || payload.Data.ErrorCode != tc.wantCode || payload.Data.Retryable != tc.wantRetry || payload.Data.DistanceM != tc.wantDist {
				t.Fatalf("response = %+v, want success=%v code=%q retryable=%v distance=%v", payload.Data, tc.wantSuccess, tc.wantCode, tc.wantRetry, tc.wantDist)
			}
		})
	}
}
