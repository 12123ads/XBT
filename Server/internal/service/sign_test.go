package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type signAuthorizationUpstream struct {
	lookup     func(string, string, int64, int64, int64) (bool, error)
	sign       func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error)
	uploadFace func(string, string, string, string, []byte) (string, error)
	checkFace  func(string, string, int64, string, string) (xxt.FaceCheckResult, error)
	detail     xxt.SignDetail
	detailErr  error
	// details overrides detail per activity id; sign-out needs two different
	// metadata records (primary and sign-out activity).
	details map[int64]xxt.SignDetail
	preSign func(string, string, xxt.FixedParams, int, string, string) (xxt.PreSignResult, error)
	// fetchClientID is the on-demand backfill hook. A nil hook returns an empty
	// clientId, which callers must treat as unsigned.
	fetchClientID func(context.Context, string, string, xxt.DeviceInfo) (string, error)
}

func (u *signAuthorizationUpstream) FetchClientIDContext(ctx context.Context, mobile, password string, info xxt.DeviceInfo) (string, error) {
	if u.fetchClientID == nil {
		return "", nil
	}
	return u.fetchClientID(ctx, mobile, password, info)
}

func (u *signAuthorizationUpstream) GetSignDetail(_ string, _ string, activityID, _ int64, _ int64, _ int64) (xxt.SignDetail, error) {
	if detail, ok := u.details[activityID]; ok {
		return detail, u.detailErr
	}
	return u.detail, u.detailErr
}

func (u *signAuthorizationUpstream) HasActivityInCourse(mobile, password string, courseID, classID, activityID int64) (bool, error) {
	if u.lookup == nil {
		panic("unexpected upstream activity lookup")
	}
	return u.lookup(mobile, password, courseID, classID, activityID)
}

func (u *signAuthorizationUpstream) PreSign(mobile, password string, fixed xxt.FixedParams, signType int, code, enc string) (xxt.PreSignResult, error) {
	if u.preSign == nil {
		return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
	}
	return u.preSign(mobile, password, fixed, signType, code, enc)
}

func (u *signAuthorizationUpstream) Sign(mobile, password string, fixed xxt.FixedParams, signType int, special map[string]interface{}) (xxt.SignResult, error) {
	if u.sign == nil {
		panic("unauthorized upstream sign")
	}
	return u.sign(mobile, password, fixed, signType, special)
}

func (u *signAuthorizationUpstream) UploadPanFile(string, string, string, string, []byte) (string, error) {
	panic("unexpected pan upload")
}

func (u *signAuthorizationUpstream) UploadFaceTemp(mobile, password, filename, contentType string, data []byte) (string, error) {
	if u.uploadFace == nil {
		panic("unexpected face upload")
	}
	return u.uploadFace(mobile, password, filename, contentType, data)
}

func (u *signAuthorizationUpstream) CheckFaceResult(mobile, password string, activityID int64, objectID, clientID string) (xxt.FaceCheckResult, error) {
	if u.checkFace == nil {
		panic("unexpected face check")
	}
	return u.checkFace(mobile, password, activityID, objectID, clientID)
}

func selectAuthorizationCourse(t *testing.T, database *gorm.DB, uid, courseID, classID int64) {
	t.Helper()
	if err := database.Create(&model.UserCourse{UserUID: uid, CourseID: courseID, ClassID: classID, IsSelected: true}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSignAuthorizesEveryTargetBeforeExposingRecords(t *testing.T) {
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("sign-test")
	for uid := int64(1); uid <= 9; uid++ {
		seedAuthorizationUser(t, database, cc, uid)
	}
	for _, uid := range []int64{1, 2, 6, 7, 8, 9} {
		selectAuthorizationCourse(t, database, uid, 10, 20)
	}
	selectAuthorizationCourse(t, database, 3, 10, 21)
	selectAuthorizationCourse(t, database, 4, 11, 20)
	groups := []model.ClassGroup{{Name: "same backend group"}, {Name: "other backend group"}}
	if err := database.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	members := []model.ClassGroupMember{{GroupID: groups[0].ID, UserUID: 1}, {GroupID: groups[0].ID, UserUID: 5}, {GroupID: groups[1].ID, UserUID: 6}}
	if err := database.Create(&members).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", 1).Update("permission", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.Whitelist{}).Where("mobile = ?", "13800000001").Update("permission", 2).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.Whitelist{}).Where("mobile = ?", "13800000007").Update("permission", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", 8).Update("permission", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Where("mobile = ?", "13800000009").Delete(&model.Whitelist{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", 3).Update("credential_cipher", "invalid-cipher").Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.SignActivityScope{ActivityID: 100, CourseID: 10, ClassID: 20}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&model.SignRecord{UserUID: 3, ActivityID: 100, SourceUID: 3, SignTimeMS: 123}).Error; err != nil {
		t.Fatal(err)
	}
	upstream := &signAuthorizationUpstream{}
	svc := NewSignService(database, upstream, cc, nil)
	for _, uid := range []int64{3, 4, 5, 7, 8, 9, 999} {
		items, err := svc.CheckSignStates(1, 100, 10, 20, []int64{2, uid})
		if !errors.Is(err, ErrSignForbidden) || items != nil {
			t.Fatalf("check target %d leaked partial state: items=%+v err=%v", uid, items, err)
		}
		result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: uid, CourseID: 10, ClassID: 20})
		if !errors.Is(err, ErrSignForbidden) || result != (SignExecuteResult{}) {
			t.Fatalf("execute target %d bypassed scope with a stored record: result=%+v err=%v", uid, result, err)
		}
	}
	var signed []int64
	upstream.sign = func(mobile, password string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
		if password != "password" || fixed.CourseID != 10 || fixed.ClassID != 20 || (mobile != "13800000002" && mobile != "13800000006") {
			t.Fatalf("incorrect authorized sign credentials or pair: mobile=%s fixed=%+v", mobile, fixed)
		}
		signed = append(signed, fixed.UID)
		return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
	}
	for _, uid := range []int64{2, 6} {
		result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: uid, CourseID: 10, ClassID: 20})
		if err != nil || !result.Success || result.AlreadySigned || result.RecordSource != 1 {
			t.Fatalf("valid same-pair target %d was denied: result=%+v err=%v", uid, result, err)
		}
	}
	items, err := svc.CheckSignStates(1, 100, 10, 20, []int64{2, 0, 6, 2, -1})
	if err != nil || len(items) != 3 || items[0].UserID != 1 || items[0].Signed || items[1].UserID != 2 || !items[1].Signed || items[2].UserID != 6 || !items[2].Signed {
		t.Fatalf("authorized check must include self, dedupe, and retain sign state: items=%+v err=%v", items, err)
	}
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20})
	if err != nil || !result.AlreadySigned || !reflect.DeepEqual(signed, []int64{2, 6}) {
		t.Fatalf("authorized existing record was not reused: result=%+v calls=%v err=%v", result, signed, err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", 1).Update("permission", 0).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20}); !errors.Is(err, ErrAccountInactive) {
		t.Fatalf("inactive operator reused signed state: %v", err)
	}
}

func TestSignScopeRequiresUpstreamEvidenceAndCachesOnlySuccess(t *testing.T) {
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("sign-scope-test")
	for _, uid := range []int64{1, 2} {
		seedAuthorizationUser(t, database, cc, uid)
		selectAuthorizationCourse(t, database, uid, 10, 20)
		selectAuthorizationCourse(t, database, uid, 11, 20)
	}
	if err := database.Create(&model.SignRecord{UserUID: 2, ActivityID: 900, SourceUID: 2, SignTimeMS: 123}).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	var lookupErr error
	lookups := 0
	upstream := &signAuthorizationUpstream{lookup: func(mobile, password string, courseID, classID, activityID int64) (bool, error) {
		lookups++
		if mobile != "13800000001" || password != "password" || (courseID != 10 && courseID != 11) || classID != 20 || activityID != 900 {
			t.Fatalf("scope lookup must use operator credentials and requested pair: %s %d/%d/%d", mobile, courseID, classID, activityID)
		}
		return found, lookupErr
	}}
	svc := NewSignService(database, upstream, cc, nil)
	if items, err := svc.CheckSignStates(1, 900, 10, 20, []int64{2}); !errors.Is(err, ErrSignForbidden) || items != nil {
		t.Fatalf("historical sign record cannot authorize a forged activity: items=%+v err=%v", items, err)
	}
	if result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 900, TargetUID: 2, CourseID: 10, ClassID: 20}); !errors.Is(err, ErrSignForbidden) || result.Success {
		t.Fatalf("already-signed shortcut bypassed activity validation: result=%+v err=%v", result, err)
	}
	found, lookupErr = true, errors.New("upstream unavailable")
	if err := svc.AuthorizeTargets(1, 900, 10, 20, []int64{2}); !errors.Is(err, ErrActivityScopeUnavailable) {
		t.Fatalf("unavailable upstream must remain distinguishable from forbidden: %v", err)
	}
	var scopes int64
	if err := database.Model(&model.SignActivityScope{}).Count(&scopes).Error; err != nil || scopes != 0 {
		t.Fatalf("failed verification persisted trusted scope: count=%d err=%v", scopes, err)
	}
	lookupErr = nil
	items, err := svc.CheckSignStates(1, 900, 10, 20, []int64{2})
	if err != nil || len(items) != 2 || !items[1].Signed {
		t.Fatalf("verified activity did not release authorized state: items=%+v err=%v", items, err)
	}
	if err := svc.AuthorizeTargets(1, 900, 11, 20, []int64{2}); err != nil {
		t.Fatalf("one activity may have two upstream-confirmed pairs: %v", err)
	}
	if err := database.Model(&model.SignActivityScope{}).Count(&scopes).Error; err != nil || scopes != 2 {
		t.Fatalf("verified activity pairs were not both retained: count=%d err=%v", scopes, err)
	}
	before := lookups
	lookupErr = errors.New("upstream offline after confirmation")
	if err := database.Model(&model.User{}).Where("uid = ?", 1).Update("credential_cipher", "expired").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CheckSignStates(1, 900, 10, 20, []int64{2}); err != nil || lookups != before {
		t.Fatalf("trusted scope unnecessarily required upstream credentials: calls=%d err=%v", lookups, err)
	}
	if err := database.Model(&model.UserCourse{}).Where("user_uid = ? AND course_id = ?", 2, 10).Update("is_selected", false).Error; err != nil {
		t.Fatal(err)
	}
	if items, err := svc.CheckSignStates(1, 900, 10, 20, []int64{2}); !errors.Is(err, ErrSignForbidden) || items != nil {
		t.Fatalf("cached activity scope bypassed current target enrollment: items=%+v err=%v", items, err)
	}
}

func TestSignDoesNotTurnRecordDatabaseFailureIntoUnsignedState(t *testing.T) {
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("sign-db-test")
	seedAuthorizationUser(t, database, cc, 1)
	selectAuthorizationCourse(t, database, 1, 10, 20)
	if err := database.Create(&model.SignActivityScope{ActivityID: 100, CourseID: 10, ClassID: 20}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Exec("ALTER TABLE sign_records RENAME TO unavailable_sign_records").Error; err != nil {
		t.Fatal(err)
	}
	svc := NewSignService(database, &signAuthorizationUpstream{}, cc, nil)
	if items, err := svc.CheckSignStates(1, 100, 10, 20, nil); err == nil || items != nil || errors.Is(err, ErrSignForbidden) {
		t.Fatalf("database failure exposed unsigned state: items=%+v err=%v", items, err)
	}
	if result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 1, CourseID: 10, ClassID: 20}); err == nil || result.Success {
		t.Fatalf("database failure proceeded to upstream sign: result=%+v err=%v", result, err)
	}
}

func TestClassContributionsScopedToViewerGroup(t *testing.T) {
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("contrib-test")
	for uid := int64(1); uid <= 6; uid++ {
		seedAuthorizationUser(t, database, cc, uid)
	}

	groupA := model.ClassGroup{Name: "Group A"}
	groupB := model.ClassGroup{Name: "Group B"}
	if err := database.Create(&groupA).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&groupB).Error; err != nil {
		t.Fatal(err)
	}
	members := []model.ClassGroupMember{
		{GroupID: groupA.ID, UserUID: 1},
		{GroupID: groupA.ID, UserUID: 2},
		{GroupID: groupA.ID, UserUID: 3},
		{GroupID: groupB.ID, UserUID: 4},
	}
	if err := database.Create(&members).Error; err != nil {
		t.Fatal(err)
	}

	// Each record needs a distinct (user_uid, activity_id) per the model's unique index.
	records := []model.SignRecord{
		// In-class proxy signs that must be counted.
		{SourceUID: 1, UserUID: 2, ActivityID: 1001, SignTimeMS: 1},
		{SourceUID: 1, UserUID: 2, ActivityID: 1002, SignTimeMS: 2},
		{SourceUID: 1, UserUID: 3, ActivityID: 1003, SignTimeMS: 3},
		{SourceUID: 2, UserUID: 3, ActivityID: 1004, SignTimeMS: 4},
		// Excluded: self sign.
		{SourceUID: 1, UserUID: 1, ActivityID: 1005, SignTimeMS: 5},
		// Excluded: 学习通自签.
		{SourceUID: -1, UserUID: 2, ActivityID: 1006, SignTimeMS: 6},
		// Excluded: source out of group.
		{SourceUID: 4, UserUID: 2, ActivityID: 1007, SignTimeMS: 7},
		// Excluded: target out of group.
		{SourceUID: 1, UserUID: 4, ActivityID: 1008, SignTimeMS: 8},
		// Excluded: both ungrouped.
		{SourceUID: 5, UserUID: 6, ActivityID: 1009, SignTimeMS: 9},
	}
	if err := database.Create(&records).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewSignService(database, &signAuthorizationUpstream{}, cc, nil)

	board, err := svc.ClassContributions(1)
	if err != nil {
		t.Fatalf("ClassContributions(1) error = %v", err)
	}
	if board.Group == nil || board.Group.ID != groupA.ID {
		t.Fatalf("expected group A, got %+v", board.Group)
	}
	if len(board.Items) != 2 {
		t.Fatalf("expected 2 ranked sources, got %+v", board.Items)
	}
	if board.Items[0].SourceUID != 1 || board.Items[0].Total != 3 {
		t.Fatalf("expected top source uid=1 total=3, got %+v", board.Items[0])
	}
	if len(board.Items[0].Details) != 2 ||
		board.Items[0].Details[0].TargetUID != 2 || board.Items[0].Details[0].Count != 2 ||
		board.Items[0].Details[1].TargetUID != 3 || board.Items[0].Details[1].Count != 1 {
		t.Fatalf("unexpected details for source 1: %+v", board.Items[0].Details)
	}
	if board.Items[1].SourceUID != 2 || board.Items[1].Total != 1 {
		t.Fatalf("expected second source uid=2 total=1, got %+v", board.Items[1])
	}

	empty, err := svc.ClassContributions(6)
	if err != nil {
		t.Fatalf("ClassContributions(6) error = %v", err)
	}
	if empty.Group != nil || len(empty.Items) != 0 {
		t.Fatalf("ungrouped viewer must get empty board, got %+v", empty)
	}
}

func TestSignExecuteResolvesIfOpenAddressFromMetadata(t *testing.T) {
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("sign-metadata-test")
	for _, uid := range []int64{1, 2} {
		seedAuthorizationUser(t, database, cc, uid)
		selectAuthorizationCourse(t, database, uid, 10, 20)
	}

	cases := []struct {
		name      string
		detail    xxt.SignDetail
		detailErr error
		requested bool
		special   map[string]interface{}
		want      bool
	}{
		{
			name:   "metadata opens address",
			detail: xxt.SignDetail{IfOpenAddress: true, LocationLatitude: 30, LocationLongitude: 120, LocationRange: 50},
			want:   true,
		},
		{
			name:      "request flag fallback when metadata fails",
			detailErr: errors.New("metadata unavailable"),
			requested: true,
			// A location sign cannot be submitted without a point once
			// metadata is unavailable (XBT-S19).
			special: map[string]interface{}{"latitude": "30", "longitude": "120"},
			want:    true,
		},
		{name: "closed address", want: false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			activityID := int64(100 + i)
			if err := database.Create(&model.SignActivityScope{ActivityID: activityID, CourseID: 10, ClassID: 20}).Error; err != nil {
				t.Fatal(err)
			}
			var got xxt.FixedParams
			upstream := &signAuthorizationUpstream{
				detail:    tc.detail,
				detailErr: tc.detailErr,
				sign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
					got = fixed
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{
				ActivityID:    activityID,
				TargetUID:     2,
				CourseID:      10,
				ClassID:       20,
				SignType:      xxt.SignLocation,
				IfOpenAddress: tc.requested,
				Special:       tc.special,
			})
			if err != nil || !result.Success || result.AlreadySigned {
				t.Fatalf("sign failed: result=%+v err=%v", result, err)
			}
			if got.IfOpenAddress != tc.want {
				t.Fatalf("IfOpenAddress=%v, want %v", got.IfOpenAddress, tc.want)
			}
		})
	}
}

func newSignStateMachineFixture(t *testing.T) (*gorm.DB, *CredentialCrypto) {
	t.Helper()
	database := newAuthorizationTestDB(t)
	cc := NewCredentialCrypto("sign-state-machine-test")
	for _, uid := range []int64{1, 2} {
		seedAuthorizationUser(t, database, cc, uid)
		selectAuthorizationCourse(t, database, uid, 10, 20)
	}
	if err := database.Create(&model.SignActivityScope{ActivityID: 100, CourseID: 10, ClassID: 20}).Error; err != nil {
		t.Fatal(err)
	}
	return database, cc
}

func seedFacePhoto(t *testing.T, database *gorm.DB, uid int64, data []byte) model.FacePhoto {
	t.Helper()
	photo := model.FacePhoto{UserUID: uid, Data: data, Source: "upload"}
	if err := database.Create(&photo).Error; err != nil {
		t.Fatal(err)
	}
	return photo
}

func TestSignExecuteFaceChallengeRetriesExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name        string
		second      xxt.SignResult
		wantSuccess bool
		wantCode    string
	}{
		{name: "recovery succeeds", second: xxt.SignResult{Kind: xxt.SignResultSuccess}, wantSuccess: true},
		{name: "second face challenge stops", second: xxt.SignResult{Kind: xxt.SignResultFaceRequired, Message: "人脸不匹配"}, wantCode: SignErrorFaceRequired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			seedFacePhoto(t, database, 2, []byte("face-bytes"))
			var calls int
			var retrySpecial map[string]interface{}
			uploads, checks := 0, 0
			upstream := &signAuthorizationUpstream{
				sign: func(_ string, _ string, _ xxt.FixedParams, _ int, special map[string]interface{}) (xxt.SignResult, error) {
					calls++
					if calls == 1 {
						if special["current_face_id"] != nil {
							t.Fatalf("first attempt carried face state: %v", special)
						}
						return xxt.SignResult{Kind: xxt.SignResultFaceRequired, Enc2: "face-enc"}, nil
					}
					retrySpecial = special
					return tc.second, nil
				},
				uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
					uploads++
					if string(data) != "face-bytes" {
						t.Fatalf("uploaded photo = %q", data)
					}
					return "face-object", nil
				},
				checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
					checks++
					if objectID != "face-object" {
						t.Fatalf("face check object = %q", objectID)
					}
					return xxt.FaceCheckResult{CurrentFaceID: "face-object", FaceEnc: "face-enc-result", FaceCode: ""}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
			if err != nil {
				t.Fatalf("ExecuteOne error = %v", err)
			}
			if calls != 2 {
				t.Fatalf("upstream calls = %d, want exactly 2", calls)
			}
			if uploads != 1 || checks != 1 {
				t.Fatalf("face recovery uploads=%d checks=%d, want one each", uploads, checks)
			}
			if retrySpecial["current_face_id"] != "face-object" || retrySpecial["enc2"] != "face-enc" {
				t.Fatalf("retry lost face state or enc2: %v", retrySpecial)
			}
			if result.Success != tc.wantSuccess || result.ErrorCode != tc.wantCode {
				t.Fatalf("result=%+v, want success=%v code=%q", result, tc.wantSuccess, tc.wantCode)
			}
		})
	}
}

func TestFaceUploadReusedWithinActivity(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, []byte("face-bytes"))
	uploads, checks := 0, 0
	var checkedObjects []string
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(_ string, _ string, _ string, _ string, data []byte) (string, error) {
			uploads++
			if string(data) != "face-bytes" && string(data) != "face-bytes-2" {
				t.Fatalf("uploaded photo = %q", data)
			}
			return fmt.Sprintf("face-object-%d", uploads), nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, _ string) (xxt.FaceCheckResult, error) {
			checks++
			checkedObjects = append(checkedObjects, objectID)
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc-" + objectID}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	sign := func() {
		t.Helper()
		result, err := svc.ExecuteOne(1, ExecuteSignRequest{
			ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
			Special: map[string]interface{}{"if_face": true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Success {
			t.Fatalf("sign failed: %+v", result)
		}
		if err := database.Where("user_uid = ? AND activity_id = ?", 2, 100).Delete(&model.SignRecord{}).Error; err != nil {
			t.Fatal(err)
		}
	}

	sign()
	sign()
	if uploads != 1 {
		t.Fatalf("uploads = %d, want one upload for two executions in the same activity", uploads)
	}
	if checks != 2 {
		t.Fatalf("face checks = %d, want one per execution", checks)
	}
	if checkedObjects[0] != "face-object-1" || checkedObjects[1] != checkedObjects[0] {
		t.Fatalf("face checks reused objectIds %v, want the single upload", checkedObjects)
	}

	if err := database.Where("user_uid = ?", 2).Delete(&model.FacePhoto{}).Error; err != nil {
		t.Fatal(err)
	}
	seedFacePhoto(t, database, 2, []byte("face-bytes-2"))
	sign()
	if uploads != 2 {
		t.Fatalf("uploads = %d, want a new upload after the photo changed", uploads)
	}
	if checkedObjects[2] != "face-object-2" {
		t.Fatalf("changed photo checked with %q", checkedObjects[2])
	}
}

func TestSignExecuteLocationErrorRetriesExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name     string
		second   xxt.SignResult
		wantCode string
	}{
		{name: "randomization retry succeeds", second: xxt.SignResult{Kind: xxt.SignResultSuccess}},
		{name: "second location error stops", second: xxt.SignResult{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 12}, wantCode: SignErrorLocationOutRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			var calls int
			upstream := &signAuthorizationUpstream{
				sign: func(_ string, _ string, _ xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
					calls++
					if calls == 1 {
						return xxt.SignResult{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 12}, nil
					}
					return tc.second, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation})
			if err != nil {
				t.Fatalf("ExecuteOne error = %v", err)
			}
			if calls != 2 {
				t.Fatalf("upstream calls = %d, want exactly 2", calls)
			}
			if tc.wantCode == "" {
				if !result.Success || result.ErrorCode != SignErrorNone || result.RecordSource != 1 {
					t.Fatalf("result=%+v, want successful proxied sign", result)
				}
				return
			}
			if result.Success || result.ErrorCode != tc.wantCode || result.DistanceM != 12 {
				t.Fatalf("result=%+v, want code=%q distance=12", result, tc.wantCode)
			}
		})
	}
}

func TestSignExecuteAlreadySignedIsRecordedAsUpstreamSource(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	var calls int
	upstream := &signAuthorizationUpstream{
		sign: func(_ string, _ string, _ xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
			calls++
			return xxt.SignResult{Kind: xxt.SignResultAlreadySigned, Message: "您已签到过了"}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
	if err != nil || !result.Success || !result.AlreadySigned || result.RecordSource != -1 || result.RecordSourceName != "学习通" {
		t.Fatalf("already-signed result=%+v err=%v", result, err)
	}
	var rec model.SignRecord
	if err := database.Where("user_uid = ? AND activity_id = ?", 2, 100).Take(&rec).Error; err != nil {
		t.Fatalf("learning-pass record missing: %v", err)
	}
	if rec.SourceUID != -1 {
		t.Fatalf("recorded source_uid = %d, want -1", rec.SourceUID)
	}

	// The local record must satisfy later checks without another upstream call.
	items, err := svc.CheckSignStates(1, 100, 10, 20, []int64{2})
	if err != nil || len(items) != 2 || !items[1].Signed || items[1].RecordSource != -1 {
		t.Fatalf("check after already_signed = %+v err=%v", items, err)
	}
	again, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
	if err != nil || !again.AlreadySigned {
		t.Fatalf("second execute = %+v err=%v", again, err)
	}
	if calls != 1 {
		t.Fatalf("upstream sign calls = %d, want 1 (no repeat after local record)", calls)
	}
}

func TestSignExecuteSuccess2WritesRecord(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	upstream := &signAuthorizationUpstream{
		sign: func(_ string, _ string, _ xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess2}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
	if err != nil || !result.Success || result.AlreadySigned || result.RecordSource != 1 {
		t.Fatalf("success2 result=%+v err=%v", result, err)
	}
	var count int64
	if err := database.Model(&model.SignRecord{}).Where("user_uid = ? AND activity_id = ? AND source_uid = ?", 2, 100, 1).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("success2 record count=%d err=%v", count, err)
	}
}

func TestSignExecuteTerminalErrorsDoNotRetry(t *testing.T) {
	cases := []struct {
		result   xxt.SignResult
		wantCode string
		wantDist float64
	}{
		{xxt.SignResult{Kind: xxt.SignResultCaptchaRequired, Enc2: "abc"}, SignErrorCaptchaRequired, 0},
		{xxt.SignResult{Kind: xxt.SignResultQRExpired}, SignErrorQRExpired, 0},
		{xxt.SignResult{Kind: xxt.SignResultSessionExpired}, SignErrorSessionExpired, 0},
		{xxt.SignResult{Kind: xxt.SignResultUpstreamError, Message: "服务器错误"}, SignErrorUpstream, 0},
		{xxt.SignResult{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 7}, SignErrorLocationOutRange, 7},
	}
	for _, tc := range cases {
		t.Run(tc.wantCode, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			calls := 0
			upstream := &signAuthorizationUpstream{
				sign: func(_ string, _ string, _ xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
					calls++
					return tc.result, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation})
			if err != nil {
				t.Fatalf("ExecuteOne error = %v", err)
			}
			// Location errors get their single protocol retry; the rest must
			// stop after the first upstream response.
			wantCalls := 1
			if tc.wantCode == SignErrorLocationOutRange {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("upstream calls = %d, want %d", calls, wantCalls)
			}
			if result.Success || result.ErrorCode != tc.wantCode || result.DistanceM != tc.wantDist {
				t.Fatalf("result=%+v, want code=%q distance=%v", result, tc.wantCode, tc.wantDist)
			}
			var count int64
			if err := database.Model(&model.SignRecord{}).Where("activity_id = ?", 100).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("failed attempt wrote %d records: %v", count, err)
			}
		})
	}
}

func TestSignExecutePreSignsEverySignTypeWithActivityExt(t *testing.T) {
	cases := []struct {
		name     string
		signType int
		special  map[string]interface{}
		wantCode string
		wantEnc  string
	}{
		{name: "normal", signType: xxt.SignNormal},
		{name: "qr", signType: xxt.SignQRCode, special: map[string]interface{}{"c": "abc", "enc": "xyz"}, wantCode: "abc", wantEnc: "xyz"},
		{name: "gesture", signType: xxt.SignGesture, special: map[string]interface{}{"sign_code": "1234"}},
		{name: "location", signType: xxt.SignLocation},
		{name: "sign code", signType: xxt.SignCode, special: map[string]interface{}{"sign_code": "5678"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			var preSignCalls int
			var preSignFixed xxt.FixedParams
			var preSignType int
			var preSignCode, preSignEnc string
			var signFixed xxt.FixedParams
			upstream := &signAuthorizationUpstream{
				detail: xxt.SignDetail{Ext: "activity-ext", SignType: tc.signType},
				preSign: func(_ string, _ string, fixed xxt.FixedParams, signType int, code, enc string) (xxt.PreSignResult, error) {
					preSignCalls++
					preSignFixed = fixed
					preSignType = signType
					preSignCode, preSignEnc = code, enc
					return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
				},
				sign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (xxt.SignResult, error) {
					signFixed = fixed
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{
				ActivityID: 100,
				TargetUID:  2,
				CourseID:   10,
				ClassID:    20,
				SignType:   tc.signType,
				Special:    tc.special,
			})
			if err != nil || !result.Success {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if preSignCalls != 1 {
				t.Fatalf("preSign calls = %d, want 1", preSignCalls)
			}
			if preSignFixed.Ext != "activity-ext" || preSignFixed.ActiveID != 100 || preSignFixed.UID != 2 {
				t.Fatalf("preSign fixed = %+v, want activity ext and target identity", preSignFixed)
			}
			if preSignType != tc.signType || preSignCode != tc.wantCode || preSignEnc != tc.wantEnc {
				t.Fatalf("preSign args type=%d code=%q enc=%q, want type=%d code=%q enc=%q",
					preSignType, preSignCode, preSignEnc, tc.signType, tc.wantCode, tc.wantEnc)
			}
			if signFixed.Ext != "activity-ext" {
				t.Fatalf("sign request lost ext: %+v", signFixed)
			}
		})
	}
}

func TestSignExecutePreSignOutcomesDoNotReachUpstreamSign(t *testing.T) {
	cases := []struct {
		name          string
		preSign       func() (xxt.PreSignResult, error)
		wantSuccess   bool
		wantAlready   bool
		wantRecord    bool
		wantCode      string
		wantSource    int64
		wantSourceTag string
	}{
		{
			name: "expired activity",
			preSign: func() (xxt.PreSignResult, error) {
				return xxt.PreSignResult{Status: xxt.PreSignExpired, Message: "签到已截止"}, nil
			},
			wantCode: SignErrorActivityExpired,
		},
		{
			name:          "upstream already signed",
			preSign:       func() (xxt.PreSignResult, error) { return xxt.PreSignResult{Status: xxt.PreSignAlreadySigned}, nil },
			wantSuccess:   true,
			wantAlready:   true,
			wantRecord:    true,
			wantSource:    -1,
			wantSourceTag: "学习通",
		},
		{
			name: "no permission",
			preSign: func() (xxt.PreSignResult, error) {
				return xxt.PreSignResult{Status: xxt.PreSignNoPermission, Message: "当前账号无权签到该活动"}, nil
			},
			wantCode: SignErrorUpstream,
		},
		{
			name: "session expired",
			preSign: func() (xxt.PreSignResult, error) {
				return xxt.PreSignResult{}, fmt.Errorf("%w: relogin rejected", xxt.ErrSessionExpired)
			},
			wantCode: SignErrorSessionExpired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			signCalls := 0
			upstream := &signAuthorizationUpstream{
				preSign: func(string, string, xxt.FixedParams, int, string, string) (xxt.PreSignResult, error) {
					return tc.preSign()
				},
				sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
					signCalls++
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal})
			if err != nil {
				t.Fatalf("ExecuteOne error = %v", err)
			}
			if result.Success != tc.wantSuccess || result.AlreadySigned != tc.wantAlready || result.ErrorCode != tc.wantCode {
				t.Fatalf("result=%+v, want success=%v already=%v code=%q", result, tc.wantSuccess, tc.wantAlready, tc.wantCode)
			}
			if tc.wantSourceTag != "" && (result.RecordSource != tc.wantSource || result.RecordSourceName != tc.wantSourceTag) {
				t.Fatalf("result source=%d/%q, want %d/%q", result.RecordSource, result.RecordSourceName, tc.wantSource, tc.wantSourceTag)
			}
			if tc.wantCode == SignErrorActivityExpired || tc.wantCode == SignErrorUpstream {
				if result.Message == "" || result.Retryable {
					t.Fatalf("terminal failure needs a readable, non-retryable message: %+v", result)
				}
			}
			if signCalls != 0 {
				t.Fatalf("stuSignajax calls = %d, preSign must short-circuit", signCalls)
			}
			var count int64
			if err := database.Model(&model.SignRecord{}).Where("activity_id = ?", 100).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			wantCount := int64(0)
			if tc.wantRecord {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("records = %d, want %d", count, wantCount)
			}
		})
	}
}

func TestSignCheckMergesUpstreamSignedStateAndPersistsOnce(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	var preSignUIDs []int64
	upstream := &signAuthorizationUpstream{
		detail: xxt.SignDetail{Ext: "activity-ext", SignType: xxt.SignCode},
		preSign: func(_ string, _ string, fixed xxt.FixedParams, signType int, _, _ string) (xxt.PreSignResult, error) {
			if fixed.Ext != "activity-ext" || signType != xxt.SignCode {
				t.Fatalf("check preSign fixed=%+v type=%d, want activity ext and type", fixed, signType)
			}
			preSignUIDs = append(preSignUIDs, fixed.UID)
			if fixed.UID == 2 {
				return xxt.PreSignResult{Status: xxt.PreSignAlreadySigned}, nil
			}
			return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)

	items, err := svc.CheckSignStates(1, 100, 10, 20, []int64{2})
	if err != nil {
		t.Fatalf("CheckSignStates error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if items[0].UserID != 1 || items[0].Signed || !items[0].CheckedUpstream {
		t.Fatalf("operator state = %+v, want unsigned and upstream-checked", items[0])
	}
	got := items[1]
	if got.UserID != 2 || !got.Signed || got.RecordSource != -1 || got.RecordSourceName != "学习通" || !got.CheckedUpstream {
		t.Fatalf("upstream-signed state = %+v, want signed with source -1/学习通", got)
	}
	if got.Message == "" {
		t.Fatalf("upstream-signed state needs a readable message")
	}
	var rec model.SignRecord
	if err := database.Where("user_uid = ? AND activity_id = ?", 2, 100).Take(&rec).Error; err != nil {
		t.Fatalf("upstream-signed record missing: %v", err)
	}
	if rec.SourceUID != -1 || rec.CourseID != 10 || rec.ClassID != 20 || rec.SignType != xxt.SignCode {
		t.Fatalf("persisted record = %+v", rec)
	}

	preSignUIDs = nil
	items, err = svc.CheckSignStates(1, 100, 10, 20, []int64{2})
	if err != nil || len(items) != 2 || !items[1].Signed || items[1].RecordSource != -1 {
		t.Fatalf("second check = %+v err=%v", items, err)
	}
	if !reflect.DeepEqual(preSignUIDs, []int64{1}) {
		t.Fatalf("second check preSign uids = %v, want only the unrecorded operator", preSignUIDs)
	}
}

func TestSignCheckExpiredActivityReportsWithoutRecord(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	upstream := &signAuthorizationUpstream{
		preSign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _, _ string) (xxt.PreSignResult, error) {
			if fixed.UID == 2 {
				return xxt.PreSignResult{Status: xxt.PreSignExpired, Message: "签到已截止"}, nil
			}
			return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	items, err := svc.CheckSignStates(1, 100, 10, 20, []int64{2})
	if err != nil || len(items) != 2 {
		t.Fatalf("items = %+v err=%v", items, err)
	}
	if got := items[1]; got.Signed || !got.CheckedUpstream || got.Message != "签到已截止" {
		t.Fatalf("expired state = %+v, want unsigned with readable closed message", got)
	}
	var count int64
	if err := database.Model(&model.SignRecord{}).Where("activity_id = ?", 100).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("expired activity wrote %d records: %v", count, err)
	}
}

func TestSignCheckPreSignFailureKeepsLocalState(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	upstream := &signAuthorizationUpstream{
		preSign: func(_ string, _ string, fixed xxt.FixedParams, _ int, _, _ string) (xxt.PreSignResult, error) {
			if fixed.UID == 2 {
				return xxt.PreSignResult{}, errors.New("upstream unreachable")
			}
			return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	items, err := svc.CheckSignStates(1, 100, 10, 20, []int64{2})
	if err != nil || len(items) != 2 {
		t.Fatalf("check must survive upstream failure: items=%+v err=%v", items, err)
	}
	if got := items[1]; got.Signed || got.CheckedUpstream || got.Message != "未签到" {
		t.Fatalf("failed merge changed local state: %+v", got)
	}
	var count int64
	if err := database.Model(&model.SignRecord{}).Where("activity_id = ?", 100).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed merge wrote %d records: %v", count, err)
	}
}

func locationMetadata() xxt.SignDetail {
	return xxt.SignDetail{
		IfOpenAddress:     true,
		LocationLatitude:  30,
		LocationLongitude: 120,
		LocationRange:     50,
	}
}

func TestSignExecuteRequiresLocationPointWithoutUsableCenter(t *testing.T) {
	for _, tc := range []struct {
		name      string
		detail    xxt.SignDetail
		detailErr error
	}{
		{
			name:   "zero center",
			detail: xxt.SignDetail{SignType: xxt.SignLocation, IfOpenAddress: true, LocationRange: 300},
		},
		{
			name:   "zero center and zero radius",
			detail: xxt.SignDetail{SignType: xxt.SignLocation, IfOpenAddress: true},
		},
		{
			name:      "metadata unavailable",
			detailErr: errors.New("getPPTActiveInfo unavailable"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			preSignCalls, signCalls := 0, 0
			upstream := &signAuthorizationUpstream{
				detail:    tc.detail,
				detailErr: tc.detailErr,
				preSign: func(string, string, xxt.FixedParams, int, string, string) (xxt.PreSignResult, error) {
					preSignCalls++
					return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
				},
				sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
					signCalls++
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{
				ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation,
			})
			if err != nil {
				t.Fatalf("ExecuteOne error = %v", err)
			}
			if result.Success || result.ErrorCode != SignErrorLocationPointRequired || result.Message != "请先在地图选点" {
				t.Fatalf("result = %+v, want location_point_required", result)
			}
			if preSignCalls != 0 || signCalls != 0 {
				t.Fatalf("missing center reached upstream: preSign=%d sign=%d", preSignCalls, signCalls)
			}
		})
	}
}

func TestSignExecuteKeepsSelectedLocationWhenCenterMissing(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	var captured map[string]interface{}
	upstream := &signAuthorizationUpstream{
		detail: xxt.SignDetail{SignType: xxt.SignLocation, IfOpenAddress: true, LocationRange: 300},
		sign: func(_ string, _ string, _ xxt.FixedParams, _ int, special map[string]interface{}) (xxt.SignResult, error) {
			captured = copySpecial(special)
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation,
		Special: map[string]interface{}{
			"latitude": "35.475740", "longitude": "119.535984", "description": "七号教学楼",
		},
	})
	if err != nil || !result.Success {
		t.Fatalf("result=%+v err=%v, want selected point accepted", result, err)
	}
	position := xxt.PositionFromSpecial(captured)
	if position == nil || position.Latitude != 35.47574 || position.Longitude != 119.535984 || position.Address != "七号教学楼" {
		t.Fatalf("selected point changed: %+v", position)
	}
}

func TestSignExecuteRejectsZeroSelectedLocationWithoutCenter(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	preSignCalls, signCalls := 0, 0
	upstream := &signAuthorizationUpstream{
		detail: xxt.SignDetail{SignType: xxt.SignLocation, IfOpenAddress: true, LocationRange: 300},
		preSign: func(string, string, xxt.FixedParams, int, string, string) (xxt.PreSignResult, error) {
			preSignCalls++
			return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
		},
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			signCalls++
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation,
		Special: map[string]interface{}{"latitude": "0", "longitude": "0"},
	})
	if err != nil {
		t.Fatalf("ExecuteOne error = %v", err)
	}
	if result.Success || result.ErrorCode != SignErrorLocationPointRequired {
		t.Fatalf("result = %+v, want location_point_required", result)
	}
	if preSignCalls != 0 || signCalls != 0 {
		t.Fatalf("zero point reached upstream: preSign=%d sign=%d", preSignCalls, signCalls)
	}
}

func TestSignExecuteRejectsPresetOutsideActivityRadius(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	signCalls, preSignCalls := 0, 0
	upstream := &signAuthorizationUpstream{
		detail: locationMetadata(),
		preSign: func(string, string, xxt.FixedParams, int, string, string) (xxt.PreSignResult, error) {
			preSignCalls++
			return xxt.PreSignResult{Status: xxt.PreSignReady}, nil
		},
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			signCalls++
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	result, err := svc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation,
		Special: map[string]interface{}{
			"latitude": "30.001", "longitude": "120", "description": "校外",
		},
	})
	if err != nil {
		t.Fatalf("ExecuteOne error = %v", err)
	}
	if result.Success || result.ErrorCode != SignErrorLocationOutRange {
		t.Fatalf("result = %+v, want location_out_of_range", result)
	}
	if result.DistanceM < 100 || result.DistanceM > 120 {
		t.Fatalf("distance_m = %v, want about 111m", result.DistanceM)
	}
	if signCalls != 0 || preSignCalls != 0 {
		t.Fatalf("out-of-range preset reached upstream: sign=%d preSign=%d", signCalls, preSignCalls)
	}
}

func TestSignExecuteSamplesLocationAndRetriesWithoutRandomization(t *testing.T) {
	for _, tc := range []struct {
		name     string
		second   xxt.SignResult
		wantCode string
		wantDist float64
	}{
		{name: "retry succeeds", second: xxt.SignResult{Kind: xxt.SignResultSuccess}},
		{name: "retry still out of range", second: xxt.SignResult{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 7}, wantCode: SignErrorLocationOutRange, wantDist: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			var attempts []map[string]interface{}
			upstream := &signAuthorizationUpstream{
				detail: locationMetadata(),
				sign: func(_ string, _ string, _ xxt.FixedParams, _ int, special map[string]interface{}) (xxt.SignResult, error) {
					attempts = append(attempts, copySpecial(special))
					if len(attempts) == 1 {
						return xxt.SignResult{Kind: xxt.SignResultLocationOutOfRange, DistanceM: 12}, nil
					}
					return tc.second, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			svc.sampleLocation = func(center xxt.GeoPoint, radiusMeters float64) xxt.GeoPoint {
				if center.Latitude != 30 || center.Longitude != 120 || radiusMeters != 50 {
					t.Fatalf("sampler args = %+v radius=%v", center, radiusMeters)
				}
				return xxt.GeoPoint{Latitude: center.Latitude + 0.0001, Longitude: center.Longitude}
			}
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{
				ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignLocation,
				Special: map[string]interface{}{
					"latitude": "30.0002", "longitude": "120.000000", "description": "教学楼",
				},
			})
			if err != nil {
				t.Fatalf("ExecuteOne error = %v", err)
			}
			if len(attempts) != 2 {
				t.Fatalf("upstream sign calls = %d, want exactly 2", len(attempts))
			}
			if got := attempts[0]["latitude"]; got != "30.000100" {
				t.Fatalf("first attempt latitude = %v, want sampled 30.000100", got)
			}
			if got := attempts[1]["latitude"]; got != "30.000200" {
				t.Fatalf("retry latitude = %v, want unrandomized preset 30.000200", got)
			}
			if got := attempts[1]["longitude"]; got != "120.000000" {
				t.Fatalf("retry longitude = %v", got)
			}
			if got := attempts[1]["description"]; got != "教学楼" {
				t.Fatalf("retry description = %v", got)
			}
			sampled := xxt.GeoPoint{Latitude: 30.0001, Longitude: 120}
			if distance := xxt.DistanceMeters(sampled, xxt.GeoPoint{Latitude: 30, Longitude: 120}); distance > 50 {
				t.Fatalf("sampled point %.2fm outside radius", distance)
			}
			if tc.wantCode == "" {
				if !result.Success {
					t.Fatalf("result = %+v, want success after retry", result)
				}
				return
			}
			if result.Success || result.ErrorCode != tc.wantCode || result.DistanceM != tc.wantDist {
				t.Fatalf("result = %+v, want code=%q distance=%v", result, tc.wantCode, tc.wantDist)
			}
		})
	}
}

func TestSignExecuteGeneratesLocationWhenPresetMissing(t *testing.T) {
	for _, signType := range []int{xxt.SignNormal, xxt.SignQRCode, xxt.SignGesture, xxt.SignLocation, xxt.SignCode} {
		t.Run(fmt.Sprint(signType), func(t *testing.T) {
			database, cc := newSignStateMachineFixture(t)
			var captured map[string]interface{}
			upstream := &signAuthorizationUpstream{
				detail: locationMetadata(),
				sign: func(_ string, _ string, _ xxt.FixedParams, _ int, special map[string]interface{}) (xxt.SignResult, error) {
					captured = copySpecial(special)
					return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
				},
			}
			svc := NewSignService(database, upstream, cc, nil)
			svc.sampleLocation = func(center xxt.GeoPoint, _ float64) xxt.GeoPoint {
				return xxt.GeoPoint{Latitude: center.Latitude + 0.00005, Longitude: center.Longitude - 0.00005}
			}
			result, err := svc.ExecuteOne(1, ExecuteSignRequest{
				ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: signType,
			})
			if err != nil || !result.Success {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if captured == nil {
				t.Fatal("upstream sign was not called")
			}
			position := xxt.PositionFromSpecial(captured)
			if position == nil {
				t.Fatalf("generated position missing from special params: %v", captured)
			}
			center := xxt.GeoPoint{Latitude: 30, Longitude: 120}
			if distance := xxt.DistanceMeters(xxt.GeoPoint{Latitude: position.Latitude, Longitude: position.Longitude}, center); distance > 50 {
				t.Fatalf("generated point %.2fm outside radius", distance)
			}
		})
	}
}

func copySpecial(special map[string]interface{}) map[string]interface{} {
	copied := make(map[string]interface{}, len(special))
	for key, value := range special {
		copied[key] = value
	}
	return copied
}

func TestFaceCheckUsesPersistedClientID(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	cipher, err := cc.Encrypt("persisted-client-id")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&model.User{}).Where("uid = ?", 2).Update("client_id_cipher", cipher).Error; err != nil {
		t.Fatal(err)
	}
	seedFacePhoto(t, database, 2, []byte("face-bytes"))
	var got []string
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, objectID, clientID string) (xxt.FaceCheckResult, error) {
			if objectID != "face-object" {
				t.Fatalf("objectID = %q", objectID)
			}
			got = append(got, clientID)
			return xxt.FaceCheckResult{CurrentFaceID: objectID, FaceEnc: "face-enc", FaceCode: ""}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	for attempt := 0; attempt < 2; attempt++ {
		result, err := svc.ExecuteOne(1, ExecuteSignRequest{
			ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
			Special: map[string]interface{}{"if_face": true},
		})
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if !result.Success {
			t.Fatalf("attempt %d: %+v", attempt, result)
		}
		if err := database.Where("user_uid = ? AND activity_id = ?", 2, 100).Delete(&model.SignRecord{}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "persisted-client-id" || got[1] != got[0] {
		t.Fatalf("face checks received clientIds %v, want the persisted value twice", got)
	}
}

func TestFaceCheckOmitsClientIDWhenStorageIsEmpty(t *testing.T) {
	database, cc := newSignStateMachineFixture(t)
	seedFacePhoto(t, database, 2, []byte("face-bytes"))
	var got string
	upstream := &signAuthorizationUpstream{
		sign: func(string, string, xxt.FixedParams, int, map[string]interface{}) (xxt.SignResult, error) {
			return xxt.SignResult{Kind: xxt.SignResultSuccess}, nil
		},
		uploadFace: func(string, string, string, string, []byte) (string, error) {
			return "face-object", nil
		},
		checkFace: func(_ string, _ string, _ int64, _, clientID string) (xxt.FaceCheckResult, error) {
			got = clientID
			return xxt.FaceCheckResult{CurrentFaceID: "face-object", FaceEnc: "face-enc"}, nil
		},
	}
	svc := NewSignService(database, upstream, cc, nil)
	if _, err := svc.ExecuteOne(1, ExecuteSignRequest{
		ActivityID: 100, TargetUID: 2, CourseID: 10, ClassID: 20, SignType: xxt.SignNormal,
		Special: map[string]interface{}{"if_face": true},
	}); err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("face check received clientId %q without stored value", got)
	}
}
