package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/middleware"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/testutil"
	"xbt2/server/internal/xxt"
)

type facePoolUpstream struct {
	data  []byte
	err   error
	calls int
}

func (u *facePoolUpstream) FetchProfileFaceContext(context.Context, string, string) ([]byte, error) {
	u.calls++
	return u.data, u.err
}

type facePoolFixture struct {
	db      *gorm.DB
	cc      *service.CredentialCrypto
	router  *gin.Engine
	tokens  map[int64]string
	service *service.FacePhotoService
}

func newFacePoolFixture(t *testing.T, upstream *facePoolUpstream) facePoolFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	database, _ := testutil.NewPostgres(t)
	if err := database.AutoMigrate(&model.User{}, &model.FacePhoto{}, &model.Whitelist{}, &model.UserCourse{}); err != nil {
		t.Fatal(err)
	}
	cc := service.NewCredentialCrypto("face-pool-handler-test")
	for _, uid := range []int64{1, 2, 3} {
		cipher, err := cc.Encrypt("password")
		if err != nil {
			t.Fatal(err)
		}
		user := model.User{UID: uid, Mobile: fmt.Sprintf("138000000%02d", uid), Name: "user", CredentialCipher: cipher, Permission: 1}
		if err := database.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.Create(&model.Whitelist{Mobile: user.Mobile, Permission: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, uid := range []int64{1, 2} {
		if err := database.Create(&model.UserCourse{UserUID: uid, CourseID: 10, ClassID: 20, IsSelected: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Create(&model.UserCourse{UserUID: 3, CourseID: 30, ClassID: 40, IsSelected: true}).Error; err != nil {
		t.Fatal(err)
	}

	jwtSvc := service.NewJWTService("face-pool-handler-jwt")
	photos := service.NewFacePhotoService(database, cc, upstream)
	authHandler := NewAuthHandler(database, jwtSvc, cc, nil)
	authHandler.SetFacePhotoService(photos)

	router := gin.New()
	authed := router.Group("")
	authed.Use(middleware.Auth(jwtSvc, database))
	authed.GET("/user/faces", authHandler.ListFaces)
	authed.POST("/user/faces", authHandler.AddFace)
	authed.DELETE("/user/faces/:id", authHandler.DeleteFaceByID)
	authed.POST("/user/faces/import", authHandler.ImportFace)

	tokens := map[int64]string{}
	for _, uid := range []int64{1, 2, 3} {
		token, err := jwtSvc.Sign(uid, fmt.Sprintf("138000000%02d", uid), 1)
		if err != nil {
			t.Fatal(err)
		}
		tokens[uid] = token
	}
	return facePoolFixture{db: database, cc: cc, router: router, tokens: tokens, service: photos}
}

func faceJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 160, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 160; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func faceMultipartRequest(t *testing.T, method, path, token string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "face.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func performFaceRequest(router http.Handler, req *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

type facePhotoResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		ID          uint   `json:"id"`
		UserUID     int64  `json:"user_uid"`
		Source      string `json:"source"`
		BaseVersion int    `json:"base_version"`
		Max         int    `json:"max"`
		Photos      []struct {
			ID              uint   `json:"id"`
			Source          string `json:"source"`
			IsFailureBefore bool   `json:"is_failure_before"`
		} `json:"photos"`
		TransformDeferred bool `json:"transform_deferred"`
	} `json:"data"`
}

func decodeFaceResponse(t *testing.T, recorder *httptest.ResponseRecorder) facePhotoResponse {
	t.Helper()
	var response facePhotoResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response %q: %v", recorder.Body.String(), err)
	}
	return response
}

func TestFacePoolRejectsSixthPhotoAndDeletesByID(t *testing.T) {
	fixture := newFacePoolFixture(t, &facePoolUpstream{})
	token := fixture.tokens[1]
	imageBytes := faceJPEG(t)
	var firstID uint
	for i := 0; i < 5; i++ {
		recorder := performFaceRequest(fixture.router, faceMultipartRequest(t, http.MethodPost, "/user/faces", token, imageBytes))
		if recorder.Code != http.StatusOK {
			t.Fatalf("upload %d status=%d body=%s", i, recorder.Code, recorder.Body.String())
		}
		response := decodeFaceResponse(t, recorder)
		if i == 0 {
			firstID = response.Data.ID
		}
	}
	recorder := performFaceRequest(fixture.router, faceMultipartRequest(t, http.MethodPost, "/user/faces", token, imageBytes))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("sixth upload status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/user/faces", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	list := decodeFaceResponse(t, performFaceRequest(fixture.router, listReq))
	if len(list.Data.Photos) != 5 || list.Data.Max != 5 {
		t.Fatalf("list = %+v", list.Data)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/user/faces/"+strconv.FormatUint(uint64(firstID), 10), nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	if recorder := performFaceRequest(fixture.router, deleteReq); recorder.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	list = decodeFaceResponse(t, performFaceRequest(fixture.router, listReq))
	if len(list.Data.Photos) != 4 {
		t.Fatalf("after delete photos=%d", len(list.Data.Photos))
	}
}

func TestFacePoolRejectsUnrelatedAccount(t *testing.T) {
	fixture := newFacePoolFixture(t, &facePoolUpstream{})
	req := httptest.NewRequest(http.MethodGet, "/user/faces?target_uid=3", nil)
	req.Header.Set("Authorization", "Bearer "+fixture.tokens[1])
	recorder := performFaceRequest(fixture.router, req)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestFacePoolImportStoresBaseImage(t *testing.T) {
	original := faceJPEG(t)
	upstream := &facePoolUpstream{data: original}
	fixture := newFacePoolFixture(t, upstream)
	req := httptest.NewRequest(http.MethodPost, "/user/faces/import?target_uid=2", nil)
	req.Header.Set("Authorization", "Bearer "+fixture.tokens[1])
	recorder := performFaceRequest(fixture.router, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeFaceResponse(t, recorder)
	if response.Data.Source != "official" || response.Data.UserUID != 2 {
		t.Fatalf("response = %+v", response.Data)
	}
	if response.Data.BaseVersion != 2 {
		t.Fatalf("imported base_version = %d, want 2", response.Data.BaseVersion)
	}
	if !response.Data.TransformDeferred {
		t.Fatal("import did not report a deferred transform")
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream calls = %d", upstream.calls)
	}
	var stored model.FacePhoto
	if err := fixture.db.Where("id = ?", response.Data.ID).Take(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored.Data, original) {
		t.Fatal("import did not store the downloaded original unchanged")
	}
}

func TestFacePoolImportWithoutOfficialPhoto(t *testing.T) {
	fixture := newFacePoolFixture(t, &facePoolUpstream{err: xxt.ErrNoProfileFacePhoto})
	req := httptest.NewRequest(http.MethodPost, "/user/faces/import?target_uid=2", nil)
	req.Header.Set("Authorization", "Bearer "+fixture.tokens[1])
	recorder := performFaceRequest(fixture.router, req)
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Message == "" {
		t.Fatal("missing readable message")
	}
}
