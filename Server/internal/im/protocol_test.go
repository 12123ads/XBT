package im

import (
	"encoding/base64"
	"testing"
)

func protoVarint(field int, value uint64) []byte {
	out := []byte{byte(field<<3 | 0)}
	for value >= 0x80 {
		out = append(out, byte(value)|0x80)
		value >>= 7
	}
	return append(out, byte(value))
}

func protoBytes(field int, value []byte) []byte {
	out := []byte{byte(field<<3 | 2)}
	length := uint64(len(value))
	for length >= 0x80 {
		out = append(out, byte(length)|0x80)
		length >>= 7
	}
	out = append(out, byte(length))
	return append(out, value...)
}

func protoKeyValue(key, value string) []byte {
	body := append(protoBytes(1, []byte(key)), protoBytes(6, []byte(value))...)
	return protoBytes(5, body)
}

func protoRoamingMessage(timestamp uint64, exts ...[]byte) string {
	var body []byte
	for _, ext := range exts {
		body = append(body, ext...)
	}
	meta := append(protoVarint(4, timestamp), protoBytes(6, body)...)
	return base64.StdEncoding.EncodeToString(meta)
}

func TestParseAttachmentAcceptsSupportedSignCard(t *testing.T) {
	payload := []byte(`{
		"attachmentType": 15,
		"att_chat_course": {
			"aid": 987654,
			"atype": 2,
			"title": "课堂签到",
			"subTitle": "10-20 10:00",
			"courseInfo": {"courseid": 222, "classid": 111, "coursename": "高等数学"}
		}
	}`)
	got, ok := ParseAttachment(payload)
	if !ok {
		t.Fatal("expected supported sign card")
	}
	want := SignActivity{
		ActiveID: 987654, Title: "课堂签到", CourseID: 222, ClassID: 111,
		CourseName: "高等数学", SubTitle: "10-20 10:00", AType: 2,
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseAttachmentDropsUnsupportedCards(t *testing.T) {
	base := `"att_chat_course": {"aid": 1, "atype": 2, "title": "t", "courseInfo": {"courseid": 2, "classid": 3}}`
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"attachment type", `{"attachmentType": 14, ` + base + `}`},
		{"missing attachment", `{"attachmentType": 15}`},
		{"atype", `{"attachmentType": 15, "att_chat_course": {"aid": 1, "atype": 3, "title": "t"}}`},
		{"atype missing", `{"attachmentType": 15, "att_chat_course": {"aid": 1, "title": "t"}}`},
		{"aid zero", `{"attachmentType": 15, "att_chat_course": {"aid": 0, "atype": 2, "title": "t"}}`},
		{"invalid json", `not-json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := ParseAttachment([]byte(tc.payload)); ok {
				t.Fatalf("accepted unsupported card: %s", tc.payload)
			}
		})
	}
}

func TestParseAttachmentAcceptsStringEncodedCourseAndNumericStrings(t *testing.T) {
	payload := []byte(`{
		"attachmentType": "15",
		"att_chat_course": "{\"aid\":\"42\",\"atype\":\"74\",\"title\":\"签退\",\"courseInfo\":{\"courseid\":\"7\",\"classid\":\"8\",\"coursename\":\"物理\"}}"
	}`)
	got, ok := ParseAttachment(payload)
	if !ok {
		t.Fatal("expected string-encoded card to parse")
	}
	if got.ActiveID != 42 || got.AType != 74 || got.CourseID != 7 || got.ClassID != 8 || got.CourseName != "物理" {
		t.Fatalf("unexpected parsed card: %+v", got)
	}
}

func TestDecodeRoamingMessageExtractsAttachment(t *testing.T) {
	attachment := `{"attachmentType":15,"att_chat_course":{"aid":123,"atype":2,"title":"签到","subTitle":"sub","courseInfo":{"courseid":9,"classid":10,"coursename":"课程"}}}`
	encoded := protoRoamingMessage(1760000000000,
		protoKeyValue("other", "ignored"),
		protoKeyValue("attachment", attachment),
	)
	got, ok := DecodeRoamingMessage(encoded)
	if !ok {
		t.Fatal("expected sign activity")
	}
	if got.ActiveID != 123 || got.CourseID != 9 || got.ClassID != 10 || got.AType != 2 || got.MessageTimeMS != 1760000000000 {
		t.Fatalf("unexpected activity: %+v", got)
	}
}

func TestDecodeRoamingMessageIgnoresTextAndMalformedValues(t *testing.T) {
	textOnly := protoRoamingMessage(1, protoKeyValue("text", "hello"))
	if _, ok := DecodeRoamingMessage(textOnly); ok {
		t.Fatal("text message produced a sign card")
	}
	if _, ok := DecodeRoamingMessage("not-base64!"); ok {
		t.Fatal("malformed base64 produced a sign card")
	}
	metaWithoutBody := base64.StdEncoding.EncodeToString(protoVarint(4, 1))
	if _, ok := DecodeRoamingMessage(metaWithoutBody); ok {
		t.Fatal("meta without body produced a sign card")
	}
	nonSignAttachment := `{"attachmentType":15,"att_chat_course":{"aid":1,"atype":3}}`
	if _, ok := DecodeRoamingMessage(protoRoamingMessage(1, protoKeyValue("attachment", nonSignAttachment))); ok {
		t.Fatal("non-sign attachment produced a sign card")
	}
}
