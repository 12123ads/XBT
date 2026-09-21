package handler

import (
	"errors"
	"regexp"
	"sort"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"xbt2/server/internal/common"
	"xbt2/server/internal/dto"
	"xbt2/server/internal/model"
)

var errWhitelistAdmin = errors.New("cannot modify admin account")

type WhitelistHandler struct {
	db *gorm.DB
}

type whitelistUserView struct {
	ID           uint   `json:"id"`
	UID          int64  `json:"uid"`
	MobileMasked string `json:"mobile_masked"`
	Permission   int    `json:"permission"`
}

func NewWhitelistHandler(db *gorm.DB) *WhitelistHandler {
	return &WhitelistHandler{db: db}
}

// ListUsers returns only ordinary whitelist users (permission=1).
func (h *WhitelistHandler) ListUsers(c *gin.Context) {
	var rows []struct {
		ID         uint
		Mobile     string
		Permission int
		UID        int64
	}
	err := h.db.Table("whitelists w").
		Select("w.id, w.mobile, w.permission, COALESCE(u.uid, 0) as uid").
		Joins("left join users u on u.mobile = w.mobile").
		Where("w.permission = ?", 1).
		Order("w.mobile asc").
		Scan(&rows).Error
	if err != nil {
		common.Fail(c, 500, "query whitelist users failed")
		return
	}

	resp := make([]whitelistUserView, 0, len(rows))
	for _, r := range rows {
		resp = append(resp, whitelistUserView{
			ID:           r.ID,
			UID:          r.UID,
			MobileMasked: common.MaskMobile(r.Mobile),
			Permission:   r.Permission,
		})
	}
	common.Success(c, resp)
}

// CreateUser only supports ordinary user whitelist entries (permission=1).
func (h *WhitelistHandler) CreateUser(c *gin.Context) {
	var req dto.AddWhitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	var row model.Whitelist
	var user model.User
	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		row, err = upsertOrdinaryWhitelistUser(tx, req.Mobile)
		if err != nil {
			return err
		}
		err = tx.Where("mobile = ?", req.Mobile).Take(&user).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	})
	if err != nil {
		if errors.Is(err, errWhitelistAdmin) {
			common.Fail(c, 400, "管理员账号不允许通过该接口修改")
		} else {
			common.Fail(c, 500, "upsert whitelist user failed")
		}
		return
	}
	common.Success(c, gin.H{
		"id":            row.ID,
		"uid":           user.UID,
		"mobile_masked": common.MaskMobile(req.Mobile),
		"permission":    1,
	})
}

// BatchImportUsers imports ordinary users by text blob.
func (h *WhitelistHandler) BatchImportUsers(c *gin.Context) {
	var req dto.BatchWhitelistRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.Fail(c, 400, "invalid request")
		return
	}

	re := regexp.MustCompile(`1\d{10}`)
	mobiles := re.FindAllString(req.Mobiles, -1)
	if len(mobiles) == 0 {
		common.Fail(c, 400, "no valid mobile number found")
		return
	}

	set := map[string]struct{}{}
	for _, m := range mobiles {
		set[m] = struct{}{}
	}
	uniq := make([]string, 0, len(set))
	for m := range set {
		uniq = append(uniq, m)
	}
	sort.Strings(uniq)

	added := 0
	skippedAdmin := 0
	err := h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		for _, mobile := range uniq {
			_, err := upsertOrdinaryWhitelistUser(tx, mobile)
			if errors.Is(err, errWhitelistAdmin) {
				skippedAdmin++
				continue
			}
			if err != nil {
				return err
			}
			added++
		}
		return nil
	})
	if err != nil {
		common.Fail(c, 500, "import whitelist users failed")
		return
	}

	common.Success(c, gin.H{
		"count":         added,
		"skipped_admin": skippedAdmin,
	})
}

// DeleteUser removes ordinary whitelist user by whitelist id.
func (h *WhitelistHandler) DeleteUser(c *gin.Context) {
	idText := c.Param("id")
	id64, err := strconv.ParseUint(idText, 10, 64)
	if err != nil || id64 == 0 {
		common.Fail(c, 400, "invalid id")
		return
	}
	id := uint(id64)

	var wl model.Whitelist
	err = h.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&wl).Error; err != nil {
			return err
		}
		if wl.Permission >= 2 {
			return errWhitelistAdmin
		}
		if err := tx.Where("id = ?", id).Delete(&model.Whitelist{}).Error; err != nil {
			return err
		}
		return tx.Model(&model.User{}).Where("mobile = ?", wl.Mobile).Update("permission", 0).Error
	})
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			common.Fail(c, 404, "not found")
		case errors.Is(err, errWhitelistAdmin):
			common.Fail(c, 400, "cannot delete admin account")
		default:
			common.Fail(c, 500, "delete failed")
		}
		return
	}

	common.Success(c, gin.H{
		"id":            id,
		"uid":           int64(0),
		"mobile_masked": common.MaskMobile(wl.Mobile),
	})
}

func upsertOrdinaryWhitelistUser(tx *gorm.DB, mobile string) (model.Whitelist, error) {
	var existing model.Whitelist
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("mobile = ?", mobile).Take(&existing).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Whitelist{}, err
	}
	if err == nil && existing.Permission >= 2 {
		return model.Whitelist{}, errWhitelistAdmin
	}
	row := model.Whitelist{Mobile: mobile, Permission: 1}
	result := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "mobile"}},
		DoUpdates: clause.AssignmentColumns([]string{"permission", "updated_at"}),
		Where: clause.Where{Exprs: []clause.Expression{
			clause.Lt{Column: clause.Column{Table: "whitelists", Name: "permission"}, Value: 2},
		}},
	}).Create(&row)
	if result.Error != nil {
		return model.Whitelist{}, result.Error
	}
	if result.RowsAffected == 0 {
		return model.Whitelist{}, errWhitelistAdmin
	}
	if err := tx.Model(&model.User{}).Where("mobile = ?", mobile).Update("permission", 1).Error; err != nil {
		return model.Whitelist{}, err
	}
	return row, nil
}
