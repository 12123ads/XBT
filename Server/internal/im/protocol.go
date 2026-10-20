package im

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SignActivity is the subset of an Easemob attachment that identifies a
// course sign card. Raw message bodies are never retained.
type SignActivity struct {
	ActiveID      int64  `json:"active_id"`
	Title         string `json:"title"`
	CourseID      int64  `json:"course_id"`
	ClassID       int64  `json:"class_id"`
	CourseName    string `json:"course_name"`
	SubTitle      string `json:"sub_title"`
	AType         int    `json:"atype"`
	MessageTimeMS int64  `json:"message_time_ms"`
}

type flexInt int64

func (f *flexInt) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*f = 0
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*f = 0
			return nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return err
		}
		*f = flexInt(n)
		return nil
	}
	n, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*f = ""
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = flexString(s)
		return nil
	}
	*f = flexString(strings.Trim(string(data), `"`))
	return nil
}

type attachmentEnvelope struct {
	AttachmentType flexInt         `json:"attachmentType"`
	ChatCourse     json.RawMessage `json:"att_chat_course"`
}

type chatCoursePayload struct {
	Aid        flexInt `json:"aid"`
	AType      flexInt `json:"atype"`
	Title      string  `json:"title"`
	SubTitle   string  `json:"subTitle"`
	CourseInfo *struct {
		CourseID   flexInt `json:"courseid"`
		ClassID    flexInt `json:"classid"`
		CourseName string  `json:"coursename"`
	} `json:"courseInfo"`
}

// ParseAttachment parses one `attachment` ext JSON value. It returns false for
// every message that is not a supported sign card (attachmentType != 15,
// missing att_chat_course, aid <= 0, or atype outside {2, 74}).
func ParseAttachment(payload []byte) (SignActivity, bool) {
	var envelope attachmentEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return SignActivity{}, false
	}
	if int64(envelope.AttachmentType) != 15 {
		return SignActivity{}, false
	}
	rawCourse := bytes.TrimSpace(envelope.ChatCourse)
	if len(rawCourse) == 0 || bytes.Equal(rawCourse, []byte("null")) {
		return SignActivity{}, false
	}
	// Some upstream versions double-encode the object as a JSON string.
	if rawCourse[0] == '"' {
		var inner string
		if err := json.Unmarshal(rawCourse, &inner); err != nil {
			return SignActivity{}, false
		}
		rawCourse = []byte(inner)
	}
	var course chatCoursePayload
	if err := json.Unmarshal(rawCourse, &course); err != nil {
		return SignActivity{}, false
	}
	if int64(course.Aid) <= 0 {
		return SignActivity{}, false
	}
	atype := int(course.AType)
	if atype != 2 && atype != 74 {
		return SignActivity{}, false
	}
	activity := SignActivity{
		ActiveID: int64(course.Aid),
		Title:    strings.TrimSpace(course.Title),
		SubTitle: strings.TrimSpace(course.SubTitle),
		AType:    atype,
	}
	if course.CourseInfo != nil {
		activity.CourseID = int64(course.CourseInfo.CourseID)
		activity.ClassID = int64(course.CourseInfo.ClassID)
		activity.CourseName = strings.TrimSpace(course.CourseInfo.CourseName)
	}
	if activity.Title == "" {
		activity.Title = "签到"
	}
	return activity, true
}

// DecodeRoamingMessage parses one base64 protobuf value from messageroaming.
// The returned bool is false when the message carries no supported sign card.
func DecodeRoamingMessage(encoded string) (SignActivity, bool) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return SignActivity{}, false
		}
	}
	meta, err := parseMeta(raw)
	if err != nil {
		return SignActivity{}, false
	}
	body, err := parseMessageBody(meta.Body)
	if err != nil {
		return SignActivity{}, false
	}
	for _, ext := range body.Ext {
		if ext.Key != "attachment" || strings.TrimSpace(ext.StringValue) == "" {
			continue
		}
		activity, ok := ParseAttachment([]byte(ext.StringValue))
		if !ok {
			continue
		}
		activity.MessageTimeMS = meta.Timestamp
		return activity, true
	}
	return SignActivity{}, false
}

type messageMeta struct {
	Timestamp int64
	Body      []byte
}

type keyValue struct {
	Key         string
	StringValue string
}

type messageBody struct {
	Ext []keyValue
}

func parseMeta(data []byte) (messageMeta, error) {
	var meta messageMeta
	err := walkProto(data, func(field int, wire int, u uint64, b []byte) error {
		switch field {
		case 4:
			if wire == 0 {
				meta.Timestamp = int64(u)
			}
		case 6:
			if wire == 2 {
				meta.Body = append(meta.Body[:0], b...)
			}
		}
		return nil
	})
	if err != nil {
		return messageMeta{}, err
	}
	if len(meta.Body) == 0 {
		return messageMeta{}, errors.New("message meta has no body")
	}
	return meta, nil
}

func parseMessageBody(data []byte) (messageBody, error) {
	var body messageBody
	err := walkProto(data, func(field int, wire int, _ uint64, b []byte) error {
		if field != 5 || wire != 2 {
			return nil
		}
		kv, err := parseKeyValue(b)
		if err != nil {
			return nil
		}
		body.Ext = append(body.Ext, kv)
		return nil
	})
	if err != nil {
		return messageBody{}, err
	}
	return body, nil
}

func parseKeyValue(data []byte) (keyValue, error) {
	var kv keyValue
	err := walkProto(data, func(field int, wire int, _ uint64, b []byte) error {
		if wire != 2 {
			return nil
		}
		switch field {
		case 1:
			kv.Key = string(b)
		case 6:
			kv.StringValue = string(b)
		}
		return nil
	})
	return kv, err
}

// walkProto decodes the protobuf wire format used by the roaming endpoint.
// Unknown fields are skipped; malformed input is rejected.
func walkProto(data []byte, fn func(field int, wire int, u uint64, b []byte) error) error {
	for len(data) > 0 {
		key, rest, err := readVarint(data)
		if err != nil {
			return fmt.Errorf("read protobuf key: %w", err)
		}
		field := int(key >> 3)
		wire := int(key & 7)
		if field <= 0 {
			return errors.New("invalid protobuf field number")
		}
		switch wire {
		case 0:
			value, next, err := readVarint(rest)
			if err != nil {
				return fmt.Errorf("read protobuf varint: %w", err)
			}
			if err := fn(field, wire, value, nil); err != nil {
				return err
			}
			data = next
		case 1:
			if len(rest) < 8 {
				return errors.New("truncated protobuf fixed64")
			}
			if err := fn(field, wire, 0, rest[:8]); err != nil {
				return err
			}
			data = rest[8:]
		case 2:
			length, next, err := readVarint(rest)
			if err != nil {
				return fmt.Errorf("read protobuf length: %w", err)
			}
			if length > uint64(len(next)) {
				return errors.New("truncated protobuf length-delimited field")
			}
			if err := fn(field, wire, 0, next[:length]); err != nil {
				return err
			}
			data = next[length:]
		case 5:
			if len(rest) < 4 {
				return errors.New("truncated protobuf fixed32")
			}
			if err := fn(field, wire, 0, rest[:4]); err != nil {
				return err
			}
			data = rest[4:]
		default:
			return fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
	}
	return nil
}

func readVarint(data []byte) (uint64, []byte, error) {
	var value uint64
	for i := 0; i < 10; i++ {
		if i >= len(data) {
			return 0, nil, errors.New("truncated protobuf varint")
		}
		b := data[i]
		value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			return value, data[i+1:], nil
		}
	}
	return 0, nil, errors.New("protobuf varint overflow")
}
