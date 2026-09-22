package service

import (
	"errors"
	"reflect"
	"testing"

	"gorm.io/gorm"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

type signAuthorizationUpstream struct {
	lookup func(string, string, int64, int64, int64) (bool, error)
	sign   func(string, string, xxt.FixedParams, int, map[string]interface{}) (string, error)
}

func (u *signAuthorizationUpstream) HasActivityInCourse(mobile, password string, courseID, classID, activityID int64) (bool, error) {
	if u.lookup == nil {
		panic("unexpected upstream activity lookup")
	}
	return u.lookup(mobile, password, courseID, classID, activityID)
}

func (u *signAuthorizationUpstream) PreSign(string, string, xxt.FixedParams, string, string) error {
	panic("unexpected QR pre-sign")
}

func (u *signAuthorizationUpstream) Sign(mobile, password string, fixed xxt.FixedParams, signType int, special map[string]interface{}) (string, error) {
	if u.sign == nil {
		panic("unauthorized upstream sign")
	}
	return u.sign(mobile, password, fixed, signType, special)
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
	upstream.sign = func(mobile, password string, fixed xxt.FixedParams, _ int, _ map[string]interface{}) (string, error) {
		if password != "password" || fixed.CourseID != 10 || fixed.ClassID != 20 || (mobile != "13800000002" && mobile != "13800000006") {
			t.Fatalf("incorrect authorized sign credentials or pair: mobile=%s fixed=%+v", mobile, fixed)
		}
		signed = append(signed, fixed.UID)
		return "success", nil
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
