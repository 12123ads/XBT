package handler

import (
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"xbt2/server/internal/model"
	"xbt2/server/internal/xxt"
)

// Activity list markers derived from upstream discovery data. They are
// additive response fields; previously returned fields keep their meaning.
const (
	ActivityStateNotStarted = "not_started"
	ActivityStateOngoing    = "ongoing"
	ActivityStateExpired    = "expired"
)

// activityExt prefers the activity metadata endpoint and falls back to the
// value carried by the discovery list. preSign must post it verbatim.
func activityExt(detail xxt.SignDetail, active xxt.Active) string {
	if detail.Ext != "" {
		return detail.Ext
	}
	return active.Ext
}

// syncActivityDiscoveryCache copies discovery-only fields into the cache row.
// It never creates a row: partial metadata must not replace a later complete
// fetch, and only the metadata path owns cache creation.
func (h *SignHandler) syncActivityDiscoveryCache(active xxt.Active, cacheFound bool, cache model.SignActivity, ext string) {
	if !cacheFound {
		return
	}
	updates := make(map[string]interface{}, 4)
	if cache.ActivityType != active.Type {
		updates["activity_type"] = active.Type
	}
	if cache.Status != active.Status {
		updates["status"] = active.Status
	}
	if cache.UserStatus != active.UserStatus {
		updates["user_status"] = active.UserStatus
	}
	if ext != "" && cache.ExtJSON != ext {
		updates["ext_json"] = ext
	}
	if len(updates) == 0 {
		return
	}
	if err := h.db.Model(&model.SignActivity{}).Where("activity_id = ?", active.ActiveID).Updates(updates).Error; err != nil {
		log.Printf("sync sign activity discovery fields failed: activity=%d err=%v", active.ActiveID, err)
	}
}

// decorateActivityItem fills the derived list markers. Upstream `type` 74 is a
// sign-out activity; the name check covers payloads that omit `type`.
func decorateActivityItem(item gin.H, now time.Time) {
	kind := activityInt(item, "type")
	if kind == 0 {
		kind = activityInt(item, "im_atype")
	}
	name, _ := item["activity_name"].(string)
	item["is_sign_out"] = kind == xxt.ActivityKindSignOut || strings.Contains(name, "签退")
	item["activity_state"] = activityState(item, now)
}

func activityState(item gin.H, now time.Time) string {
	start := activityInt64(item, "start_time")
	end := activityInt64(item, "end_time")
	status := activityInt(item, "status")
	nowMS := now.UnixMilli()
	switch {
	case end > 0 && nowMS > end:
		return ActivityStateExpired
	case start > 0 && nowMS < start:
		return ActivityStateNotStarted
	case status != 0 && status != 1:
		return ActivityStateExpired
	default:
		return ActivityStateOngoing
	}
}

func activityInt64(item gin.H, key string) int64 {
	switch value := item[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func activityInt(item gin.H, key string) int {
	return int(activityInt64(item, key))
}
