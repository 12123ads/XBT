package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/faker"
	"xbt2/server/internal/middleware"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const (
	fakerSelfUID   = int64(10001)
	fakerOtherUID  = int64(10002)
	fakerAdminUID  = int64(10003)
	fakerTestKey   = "faker-test-aes-key"
	fakerTestCreds = "faker-test-credential-secret"
)

type fakerExportFixture struct {
	db     *gorm.DB
	cc     *service.CredentialCrypto
	xxt    *xxt.Client
	tokens map[int64]string
	router http.Handler
}

func newFakerExportFixture(t *testing.T) *fakerExportFixture {
	t.Helper()
	database := newHandlerAccessDB(t)
	cc := service.NewCredentialCrypto(fakerTestCreds)
	client := xxt.New(fakerTestKey, "", "", false, 10)

	accounts := []struct {
		uid        int64
		mobile     string
		name       string
		password   string
		permission int
	}{
		{fakerSelfUID, "13800000001", "本人", "self-secret-password", 1},
		{fakerOtherUID, "13800000002", "同学", "other-secret-password", 1},
		{fakerAdminUID, "13800000003", "管理员", "admin-secret-password", 2},
	}
	for _, account := range accounts {
		cipher, err := cc.Encrypt(account.password)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&model.Whitelist{Mobile: account.mobile, Permission: account.permission}).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&model.User{
			UID:              account.uid,
			Mobile:           account.mobile,
			Name:             account.name,
			CredentialCipher: cipher,
			Permission:       account.permission,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	jwtSvc := service.NewJWTService("faker-test-jwt")
	tokens := make(map[int64]string, len(accounts))
	for _, account := range accounts {
		token, err := jwtSvc.Sign(account.uid, account.mobile, account.permission)
		if err != nil {
			t.Fatal(err)
		}
		tokens[account.uid] = token
	}

	h := NewFakerExportHandler(database, client, cc)
	router := gin.New()
	api := router.Group("/api")
	api.Use(middleware.Auth(jwtSvc, database))
	api.GET("/account/faker-export", h.Self)
	admin := api.Group("/admin")
	admin.Use(middleware.AdminOnly())
	admin.GET("/accounts/faker-export", h.AdminList)
	admin.GET("/accounts/faker-export.zip", h.AdminZip)

	return &fakerExportFixture{db: database, cc: cc, xxt: client, tokens: tokens, router: router}
}

func (f *fakerExportFixture) get(t *testing.T, path string, uid int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+f.tokens[uid])
	recorder := httptest.NewRecorder()
	f.router.ServeHTTP(recorder, req)
	return recorder
}

func decodeFakerSelf(t *testing.T, recorder *httptest.ResponseRecorder) fakerExportSelfResponse {
	t.Helper()
	var response struct {
		Code int                     `json:"code"`
		Data fakerExportSelfResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode self export %q: %v", recorder.Body.String(), err)
	}
	if response.Code != 0 {
		t.Fatalf("self export code = %d", response.Code)
	}
	return response.Data
}

func decodeFakerBatch(t *testing.T, recorder *httptest.ResponseRecorder) fakerExportBatchResponse {
	t.Helper()
	var response struct {
		Code int                      `json:"code"`
		Data fakerExportBatchResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode batch export %q: %v", recorder.Body.String(), err)
	}
	if response.Code != 0 {
		t.Fatalf("batch export code = %d", response.Code)
	}
	return response.Data
}

func TestFakerExportSelfOnlyExportsJWTAccount(t *testing.T) {
	fixture := newFakerExportFixture(t)
	recorder := fixture.get(t, "/api/account/faker-export", fakerSelfUID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	response := decodeFakerSelf(t, recorder)
	if len(response.Warnings) == 0 {
		t.Fatal("warnings must be present")
	}
	if !strings.HasPrefix(response.QRPng, "data:image/png;base64,") {
		t.Fatalf("qr_png = %q", response.QRPng)
	}

	parsed, err := url.Parse(response.URL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("phone") != "13800000001" {
		t.Fatalf("phone = %q", query.Get("phone"))
	}
	if query.Get("name") != "本人" {
		t.Fatalf("name = %q", query.Get("name"))
	}
	if query.Get("dc") != fixture.xxt.DeviceCode(fakerSelfUID) {
		t.Fatalf("dc = %q", query.Get("dc"))
	}
	wantPassword, err := faker.EncryptPassword("self-secret-password")
	if err != nil {
		t.Fatal(err)
	}
	if query.Get("pwd") != wantPassword {
		t.Fatalf("pwd does not decrypt to the account password")
	}
	if _, ok := query["face"]; !ok || query.Get("face") != "" {
		t.Fatalf("face must be present and empty: %v", query)
	}

	other := fixture.get(t, "/api/account/faker-export", fakerOtherUID)
	otherURL, err := url.Parse(decodeFakerSelf(t, other).URL)
	if err != nil {
		t.Fatal(err)
	}
	if otherURL.Query().Get("phone") != "13800000002" {
		t.Fatalf("other phone = %q", otherURL.Query().Get("phone"))
	}
}

func TestFakerExportAdminBatchRejectsNonAdmin(t *testing.T) {
	fixture := newFakerExportFixture(t)
	recorder := fixture.get(t, "/api/admin/accounts/faker-export", fakerSelfUID)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, leaked := range []string{"13800000001", "13800000002", "本人", "同学", "cdn.aquamarine5.fun"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("403 body leaked %q: %s", leaked, body)
		}
	}
}

func TestFakerExportAdminBatchFiltersUIDs(t *testing.T) {
	fixture := newFakerExportFixture(t)
	recorder := fixture.get(t, "/api/admin/accounts/faker-export?uids=10002,10001", fakerAdminUID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	response := decodeFakerBatch(t, recorder)
	if len(response.Items) != 2 {
		t.Fatalf("items = %d: %+v", len(response.Items), response.Items)
	}
	if response.Items[0].UID != fakerOtherUID || response.Items[1].UID != fakerSelfUID {
		t.Fatalf("uids not filtered/ordered as requested: %+v", response.Items)
	}
	for _, item := range response.Items {
		if item.Error != "" || item.URL == "" || item.QRPng == "" {
			t.Fatalf("incomplete item: %+v", item)
		}
		if item.Phone == "" || item.Name == "" {
			t.Fatalf("missing account metadata: %+v", item)
		}
	}

	missing := fixture.get(t, "/api/admin/accounts/faker-export?uids=10001,99999", fakerAdminUID)
	missingBody := decodeFakerBatch(t, missing)
	if len(missingBody.Items) != 2 || missingBody.Items[1].UID != 99999 || missingBody.Items[1].Error == "" {
		t.Fatalf("missing uid handling: %+v", missingBody.Items)
	}
}

func TestFakerExportAdminBatchRejectsInvalidUIDs(t *testing.T) {
	fixture := newFakerExportFixture(t)
	for _, query := range []string{"?uids=abc", "?uids=1,,2", "?uids=0", "?uids=-3"} {
		recorder := fixture.get(t, "/api/admin/accounts/faker-export"+query, fakerAdminUID)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d", query, recorder.Code)
		}
	}
}

func TestFakerExportAdminZipContainsOnePNGPerAccount(t *testing.T) {
	fixture := newFakerExportFixture(t)
	recorder := fixture.get(t, "/api/admin/accounts/faker-export.zip?uids=10001,10002", fakerAdminUID)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/zip" {
		t.Fatalf("Content-Type = %q", got)
	}

	zr, err := zip.NewReader(bytes.NewReader(recorder.Body.Bytes()), int64(recorder.Body.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, file := range zr.File {
		files[file.Name] = file
	}

	for _, uid := range []int64{fakerSelfUID, fakerOtherUID} {
		file, ok := files["qr-"+strconv.FormatInt(uid, 10)+".png"]
		if !ok {
			t.Fatalf("missing qr-%d.png in %v", uid, files)
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(reader)
		reader.Close()
		if err != nil {
			t.Fatalf("qr-%d.png is not a PNG: %v", uid, err)
		}
		if b := img.Bounds(); b.Dx() != faker.QRSize || b.Dy() != faker.QRSize {
			t.Fatalf("qr-%d size = %dx%d", uid, b.Dx(), b.Dy())
		}
	}
	if _, ok := files["qr-10003.png"]; ok {
		t.Fatal("zip must contain only the requested accounts")
	}

	links, ok := files["links.txt"]
	if !ok {
		t.Fatal("links.txt missing")
	}
	reader, err := links.Open()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("links.txt lines = %d: %q", len(lines), raw)
	}
	for index, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Fatalf("line %d columns = %d: %q", index, len(fields), line)
		}
		if fields[3] == "" || !strings.HasPrefix(fields[3], "http://cdn.aquamarine5.fun/?") {
			t.Fatalf("line %d url = %q", index, fields[3])
		}
	}
}

func TestFakerExportLogsNeverContainPassword(t *testing.T) {
	fixture := newFakerExportFixture(t)
	var logs bytes.Buffer
	original := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(original)

	if recorder := fixture.get(t, "/api/account/faker-export", fakerSelfUID); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if recorder := fixture.get(t, "/api/admin/accounts/faker-export", fakerAdminUID); recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}

	output := logs.String()
	if strings.Contains(output, "self-secret-password") || strings.Contains(output, "pwd=") {
		t.Fatalf("secret leaked into logs: %s", output)
	}
	if !strings.Contains(output, "faker export uid=") {
		t.Fatalf("expected audit log line, got: %s", output)
	}
}
