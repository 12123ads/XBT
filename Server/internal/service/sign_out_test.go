package service

import (
	"testing"
	"time"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

func newSignOutFixture(t *testing.T) (*gorm.DB, *CredentialCrypto, *signAuthorizationUpstream) {
	t.Helper()
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("sign-out-test")
	seedAuthorizationUser(t, database, cc, 1)
	selectAuthorizationCourse(t, database, 1, 10, 20)
	if err := database.Create(&model.SignActivityScope{ActivityID: 100, CourseID: 10, ClassID: 20}).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &signAuthorizationUpstream{details: map[int64]xxt.SignDetail{
		100: {SignType: xxt.SignNormal, SignOutID: 200, SignOutPublishTime: time.Now().Add(-time.Minute).UnixMilli()},
		200: {SignType: xxt.SignNormal, SignInID: 100},
	}}
	return database, cc, upstream
}

func seedPrimarySignRecord(t *testing.T, database *gorm.DB) {
	t.Helper()
	if err := database.Create(&model.SignRecord{
		UserUID:    1,
		ActivityID: 100,
		SourceUID:  1,
		CourseID:   10,
		ClassID:    20,
		SignType:   xxt.SignNormal,
		SignTimeMS: 1234,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func signOutRequest() SignOutRequest {
	return SignOutRequest{ActivityID: 100, TargetUID: 1, CourseID: 10, ClassID: 20}
}

func TestSignOutRequiresPrimarySignIn(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	upstream.sign = func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
		t.Fatal("sign-out must not reach upstream without a primary sign-in")
		return xxt.SignResult{}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	result, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ErrorCode != SignErrorSignInRequired {
		t.Fatalf("unsigned user accepted: %+v", result)
	}
	var count int64
	if err := database.Model(&model.SignOutRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected sign-out wrote %d rows", count)
	}
}

func TestSignOutWritesIndependentRecord(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	seedPrimarySignRecord(t, database)
	var signedActivity int64
	upstream.sign = func(_ string, _ string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
		signedActivity = fixed.ActiveID
		return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	result, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.AlreadySignedOut || result.ErrorCode != "" {
		t.Fatalf("sign-out failed: %+v", result)
	}
	if signedActivity != 200 || result.SignOutID != 200 || result.SignOutTimeMS <= 0 {
		t.Fatalf("sign-out submitted activity=%d result=%+v", signedActivity, result)
	}

	var record model.SignOutRecord
	if err := database.Where("user_uid = ? AND activity_id = ?", 1, 100).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.SignOutID != 200 || record.SourceUID != 1 || record.SignType != xxt.SignNormal || record.SignOutTimeMS == 0 {
		t.Fatalf("unexpected sign-out record: %+v", record)
	}
	var signIn model.SignRecord
	if err := database.Where("user_uid = ? AND activity_id = ?", 1, 100).Take(&signIn).Error; err != nil {
		t.Fatalf("sign-in record was overwritten: %v", err)
	}
	if signIn.SourceUID != 1 || signIn.SignTimeMS != 1234 {
		t.Fatalf("sign-in record changed: %+v", signIn)
	}
}

func TestSignOutRepeatsIdempotently(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	seedPrimarySignRecord(t, database)
	signCalls := 0
	upstream.sign = func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
		signCalls++
		return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	first, err := svc.SignOutOne(1, signOutRequest())
	if err != nil || !first.Success {
		t.Fatalf("first sign-out: result=%+v err=%v", first, err)
	}
	second, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !second.Success || !second.AlreadySignedOut || second.SignOutID != 200 {
		t.Fatalf("repeat was not idempotent: %+v", second)
	}
	if signCalls != 1 {
		t.Fatalf("repeat reached upstream %d times", signCalls)
	}
	var count int64
	if err := database.Model(&model.SignOutRecord{}).Where("user_uid = ? AND activity_id = ?", 1, 100).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate sign-out rows: %d", count)
	}
}

func TestSignOutMapsUpstreamError(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	seedPrimarySignRecord(t, database)
	upstream.sign = func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
		return xxt.SignResult{Kind: xxt.SignResultUpstreamError, Message: "签到失败，请重新扫描"}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	result, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ErrorCode != SignErrorUpstream || !result.Retryable {
		t.Fatalf("upstream error not mapped: %+v", result)
	}
	var count int64
	if err := database.Model(&model.SignOutRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("failed sign-out wrote %d rows", count)
	}
}

func TestSignOutRejectsBeforePublish(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	seedPrimarySignRecord(t, database)
	upstream.details = map[int64]xxt.SignDetail{
		100: {SignType: xxt.SignNormal, SignOutID: 0, SignOutPublishTime: time.Now().Add(time.Hour).UnixMilli()},
	}
	upstream.sign = func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
		t.Fatal("unpublished sign-out reached upstream")
		return xxt.SignResult{}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	result, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ErrorCode != SignErrorSignOutUnpublished {
		t.Fatalf("unpublished sign-out accepted: %+v", result)
	}
}

func TestSignOutRecordsUpstreamAlreadySignedOut(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	seedPrimarySignRecord(t, database)
	upstream.preSign = func(_ string, _ string, fixed xxt.FixedParams, _ int, _, _ string) (xxt.PreSignResult, error) {
		if fixed.ActiveID == 200 {
			return xxt.PreSignResult{Status: xxt.PreSignAlreadySigned, Message: "已经签退过了"}, nil
		}
		return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
	}
	upstream.sign = func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
		t.Fatal("already signed-out user reached stuSignajax")
		return xxt.SignResult{}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	result, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || !result.AlreadySignedOut || result.SignOutID != 200 {
		t.Fatalf("upstream already signed out not recorded: %+v", result)
	}
	var record model.SignOutRecord
	if err := database.Where("user_uid = ? AND activity_id = ?", 1, 100).Take(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.SourceUID != -1 || record.SignOutID != 200 {
		t.Fatalf("unexpected upstream-source record: %+v", record)
	}
}

func TestSignOutWithoutSignOutActivityIsUnavailable(t *testing.T) {
	database, cc, upstream := newSignOutFixture(t)
	seedPrimarySignRecord(t, database)
	upstream.details = map[int64]xxt.SignDetail{100: {SignType: xxt.SignNormal}}
	upstream.sign = func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
		t.Fatal("activity without signOutId reached upstream")
		return xxt.SignResult{}, nil
	}
	svc := NewSignService(database, upstream, cc, nil)

	result, err := svc.SignOutOne(1, signOutRequest())
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.ErrorCode != SignErrorSignOutUnavailable {
		t.Fatalf("activity without sign-out accepted: %+v", result)
	}
}
