package handler

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"xbt2/server/internal/common"
	"xbt2/server/internal/faker"
	"xbt2/server/internal/model"
	"xbt2/server/internal/service"
	"xbt2/server/internal/xxt"
)

const fakerExportCredentialError = "credential expired, please add account again"

var fakerExportWarnings = []string{
	"链接等同于账号密码，拿到即可还原密码并控制账号，请勿转发或截图外传",
}

type FakerExportHandler struct {
	db  *gorm.DB
	xxt *xxt.Client
	cc  *service.CredentialCrypto
}

type fakerExportItem struct {
	UID   int64  `json:"uid"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
	URL   string `json:"url"`
	QRPng string `json:"qr_png"`
	Error string `json:"error,omitempty"`
}

type fakerExportSelfResponse struct {
	URL      string   `json:"url"`
	QRPng    string   `json:"qr_png"`
	Warnings []string `json:"warnings"`
}

type fakerExportBatchResponse struct {
	Items    []fakerExportItem `json:"items"`
	Warnings []string          `json:"warnings"`
}

type fakerExportEntry struct {
	user model.User
	link string
	png  []byte
}

func NewFakerExportHandler(db *gorm.DB, xxtClient *xxt.Client, cc *service.CredentialCrypto) *FakerExportHandler {
	return &FakerExportHandler{db: db, xxt: xxtClient, cc: cc}
}

// Self exports only the account carried by the request JWT.
func (h *FakerExportHandler) Self(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid := common.GetUserUID(c)
	user, err := service.LoadActiveUser(h.db.WithContext(c.Request.Context()), uid)
	if err != nil {
		if errors.Is(err, service.ErrAccountInactive) {
			common.Fail(c, 403, "account inactive")
		} else {
			common.Fail(c, 500, "query account failed")
		}
		return
	}

	entry, err := h.buildEntry(user)
	if err != nil {
		if errors.Is(err, errFakerCredential) {
			common.Fail(c, 400, fakerExportCredentialError)
		} else {
			common.Fail(c, 500, "build faker export failed")
		}
		return
	}

	log.Printf("faker export uid=%d targets=1 ip=%s", uid, c.ClientIP())
	common.Success(c, fakerExportSelfResponse{
		URL:      entry.link,
		QRPng:    faker.DataURL(entry.png),
		Warnings: fakerExportWarnings,
	})
}

// AdminList exports the requested uids, or every stored account when uids is
// empty. Per-account failures are reported inline and never abort the batch.
func (h *FakerExportHandler) AdminList(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uids, err := parseFakerUIDs(c.Query("uids"))
	if err != nil {
		common.Fail(c, 400, "invalid uids")
		return
	}

	items, err := h.collect(c, uids)
	if err != nil {
		common.Fail(c, 500, "query accounts failed")
		return
	}

	log.Printf("faker export uid=%d targets=%d ip=%s", common.GetUserUID(c), len(items), c.ClientIP())
	common.Success(c, fakerExportBatchResponse{Items: items, Warnings: fakerExportWarnings})
}

// AdminZip streams one PNG per account plus links.txt in a single archive.
func (h *FakerExportHandler) AdminZip(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uids, err := parseFakerUIDs(c.Query("uids"))
	if err != nil {
		common.Fail(c, 400, "invalid uids")
		return
	}

	users, err := h.loadUsers(c, uids)
	if err != nil {
		common.Fail(c, 500, "query accounts failed")
		return
	}

	entries := make([]fakerExportEntry, 0, len(users))
	for _, user := range users {
		entry, err := h.buildEntry(user)
		if err != nil {
			log.Printf("faker export zip skipped uid=%d error=%v", user.UID, err)
			continue
		}
		entries = append(entries, entry)
	}

	archive, err := buildFakerZip(entries)
	if err != nil {
		common.Fail(c, 500, "build export archive failed")
		return
	}

	log.Printf("faker export uid=%d targets=%d ip=%s", common.GetUserUID(c), len(entries), c.ClientIP())
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="faker-export.zip"`)
	c.Data(http.StatusOK, "application/zip", archive)
}

func (h *FakerExportHandler) collect(c *gin.Context, uids []int64) ([]fakerExportItem, error) {
	users, err := h.loadUsers(c, uids)
	if err != nil {
		return nil, err
	}

	if len(uids) == 0 {
		items := make([]fakerExportItem, 0, len(users))
		for _, user := range users {
			items = append(items, h.itemFor(user))
		}
		return items, nil
	}

	byUID := make(map[int64]model.User, len(users))
	for _, user := range users {
		byUID[user.UID] = user
	}
	items := make([]fakerExportItem, 0, len(uids))
	for _, uid := range uids {
		user, ok := byUID[uid]
		if !ok {
			items = append(items, fakerExportItem{UID: uid, Error: "account not found"})
			continue
		}
		items = append(items, h.itemFor(user))
	}
	return items, nil
}

func (h *FakerExportHandler) loadUsers(c *gin.Context, uids []int64) ([]model.User, error) {
	query := h.db.WithContext(c.Request.Context()).Order("uid asc")
	if len(uids) > 0 {
		query = query.Where("uid IN ?", uids)
	}
	var users []model.User
	if err := query.Find(&users).Error; err != nil {
		return nil, err
	}
	return users, nil
}

func (h *FakerExportHandler) itemFor(user model.User) fakerExportItem {
	item := fakerExportItem{UID: user.UID, Name: user.Name, Phone: user.Mobile}
	entry, err := h.buildEntry(user)
	if err != nil {
		if errors.Is(err, errFakerCredential) {
			item.Error = fakerExportCredentialError
		} else {
			item.Error = "build faker export failed"
		}
		return item
	}
	item.URL = entry.link
	item.QRPng = faker.DataURL(entry.png)
	return item
}

var errFakerCredential = errors.New("faker: credential decrypt failed")

func (h *FakerExportHandler) buildEntry(user model.User) (fakerExportEntry, error) {
	password, err := h.cc.Decrypt(user.CredentialCipher)
	if err != nil {
		return fakerExportEntry{}, errFakerCredential
	}
	link, err := faker.BuildURL(user.Mobile, password, user.Name, h.xxt.DeviceCode(user.UID))
	if err != nil {
		return fakerExportEntry{}, err
	}
	png, err := faker.QRPNG(link)
	if err != nil {
		return fakerExportEntry{}, err
	}
	return fakerExportEntry{user: user, link: link, png: png}, nil
}

func parseFakerUIDs(raw string) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("empty uid")
		}
		uid, err := strconv.ParseInt(part, 10, 64)
		if err != nil || uid <= 0 {
			return nil, errors.New("invalid uid")
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	return out, nil
}

func buildFakerZip(entries []fakerExportEntry) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	for _, entry := range entries {
		file, err := writer.Create(fmt.Sprintf("qr-%d.png", entry.user.UID))
		if err != nil {
			return nil, err
		}
		if _, err := file.Write(entry.png); err != nil {
			return nil, err
		}
	}

	links, err := writer.Create("links.txt")
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		line := fmt.Sprintf("%d\t%s\t%s\t%s\n",
			entry.user.UID,
			singleLine(entry.user.Mobile),
			singleLine(entry.user.Name),
			entry.link,
		)
		if _, err := links.Write([]byte(line)); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func singleLine(value string) string {
	return strings.NewReplacer("\t", " ", "\r", " ", "\n", " ").Replace(value)
}
